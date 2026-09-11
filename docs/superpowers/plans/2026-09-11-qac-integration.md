# QAC Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one active Ghost WorkItem move between configured cognitive resources through QAC decisions.

**Architecture:** `internal/cognition` owns WorkItem state, QAC snapshots, turn buffers, budgets, and transition plans. `internal/app` remains the only runtime event reader and performs sends before committing a plan. QAC remains a pure dependency.

**Tech Stack:** Go 1.26.5, Bubble Tea v2, QAC v0.1.0, Jujutsu.

**Spec:** `docs/superpowers/specs/2026-09-11-qac-integration-design.md`

## Global Constraints

- Keep QAC free of Ghost runtime side effects.
- Keep `internal/app` as the only `Session.Events()` reader.
- Support one active QAC WorkItem only.
- Do not add provider, persistence, queue, live-quota, or automatic-completion features.
- Use QAC default threshold configuration only.
- Preserve manual per-agent steering and do not charge it to QAC budgets.
- Use `gofmt`, focused tests, `make test`, `make vet`, `make build`, and race tests.
- Use Conventional Commit messages and Jujutsu changes.

---

### Task 1: Add the local QAC dependency and strict Ghost configuration

**Files:**
- Modify: `go.mod`, `go.sum`, `internal/config/config.go`, `internal/config/config_test.go`, `ghost.toml.example`
- Create: `internal/config/qac_test.go`

**Interfaces:**
- Produces `config.QACConfig`, `config.QACResourceConfig`, and parsed `time.Duration` cooldown values.
- Produces `config.ValidateQAC() error`, called from `validate` only when QAC is enabled.

- [ ] Write table tests for valid three-tier TOML, disabled legacy config, missing entry resource, duplicate mapped agent, invalid `[0,1]` value, negative activation limit, bad cooldown, and unknown policy type.
- [ ] Run `go test ./internal/config -run QAC`; expect compile failure because QAC configuration does not exist.
- [ ] Add the local development requirement and replacement:

```go
require github.com/haha-systems/qac v0.0.0
replace github.com/haha-systems/qac => ../qac
```

- [ ] Add config types and validate `enabled`, `entry_resource`, `policy.type == "threshold"`, unique non-empty hierarchy, distinct mapped agents, configured agents, finite normalized economics, default importance, non-negative limits, and non-negative parsed cooldown.
- [ ] Construct `threshold.New(threshold.Config{Hierarchy: cfg.QAC.Policy.Hierarchy})` during validation and wrap its error.
- [ ] Add commented `[qac]`, policy, resource, and Veil budget examples without replacing user configuration.
- [ ] Run `gofmt -w internal/config`; run `go mod tidy`; run `go test ./internal/config`.
- [ ] Commit: `feat(config): add qac configuration`.

### Task 2: Build the cognition domain, snapshots, and budgets

**Files:**
- Create: `internal/cognition/config.go`, `internal/cognition/work.go`, `internal/cognition/resource.go`, `internal/cognition/budget.go`, `internal/cognition/coordinator.go`, `internal/cognition/coordinator_test.go`

**Interfaces:**
- Consumes `config.QACConfig` and `runtime.Session` snapshots.
- Produces `Coordinator`, `WorkItem`, `SessionSnapshot`, `StartWork`, `ResourceSnapshot`, and `BudgetSnapshot`.

- [ ] Write tests that show idle destination availability, failed/stopped/starting/unrelated-busy unavailability, current-owner representation, entry activation only after commit, exhaustion, and cooldown.
- [ ] Run `go test ./internal/cognition -run 'Snapshot|Budget|StartWork'`; expect failure because the package does not exist.
- [ ] Define `WorkState` (`active`, `paused`, `done`) and `WorkItem` with ID, goal, importance, owner resource/agent, timestamps, and handoff count.
- [ ] Define immutable resource bindings from config. Build `qac.Resource` on every evaluation; set `Available` from current session state and work ownership.
- [ ] Track activation count and last activation time. Build QAC budget entries with `Enabled: true`; omit unbounded resources; calculate `RemainingInvocations` and `Cooldown` without timers.
- [ ] Implement `StartWork(goal)` as a plan for the entry binding, with no ownership or budget mutation before commit.
- [ ] Run `gofmt -w internal/cognition`; run `go test ./internal/cognition`.
- [ ] Commit: `feat(cognition): add work and budget state`.

### Task 3: Add the QAC request parser and handoff packet

**Files:**
- Create: `internal/cognition/request.go`, `internal/cognition/parser.go`, `internal/cognition/handoff.go`, `internal/cognition/parser_test.go`
- Modify: `GHOST.md`

**Interfaces:**
- Produces `ParseQACRequest(text string) (request QACRequest, visible string, found bool, err error)`.
- Produces `BuildHandoff(work WorkItem, plan TransitionPlan) string`.

- [ ] Write tests for one valid escalation, one release, no marker, malformed JSON, two markers, out-of-range signals, and forbidden `importance`.
- [ ] Write handoff tests that require goal, source/destination, decision, evidence, unresolved question, attempted work, recommended focus, and reject copied transcript text.
- [ ] Run `go test ./internal/cognition -run 'Parse|Handoff'`; expect failure because parser symbols do not exist.
- [ ] Parse only `<QAC_REQUEST>` blocks with `encoding/json`; reject unknown `importance` by decoding into an auxiliary raw map before decoding the request type.
- [ ] Return machine-block-stripped visible text only after successful validation; preserve original text on every parser error.
- [ ] Build the fixed `[QAC HANDOFF]` frontier packet from structured request data.
- [ ] Update `GHOST.md` with the one-block, normalized-signal, no-importance, no-named-destination protocol.
- [ ] Run `gofmt -w internal/cognition`; run `go test ./internal/cognition`.
- [ ] Commit: `feat(cognition): parse qac requests and build handoffs`.

### Task 4: Preserve terminal Codex turn identity and backend method

**Files:**
- Modify: `internal/codex/session.go`, `internal/codex/notification_test.go`

**Interfaces:**
- Runtime terminal events carry the completed `TurnID`.
- All normalized Codex events contain `Metadata["backend_method"]`.

- [ ] Add a failing terminal-event test that starts a known turn, handles `turn/completed`, and requires the event's turn ID to equal the completed ID after the guard becomes idle.
- [ ] Add tests for `turn/failed` and backend method metadata on message and non-message events.
- [ ] Run `go test ./internal/codex -run 'Turn|Metadata'`; expect failure on the old empty terminal ID.
- [ ] Capture `id := turnID(p)` before `guard.Complete`, then emit `TurnID: id` for terminal events; use the active ID only for non-terminal events.
- [ ] Set `Metadata: map[string]string{"backend_method": n.Method}` without changing raw payload handling.
- [ ] Run `gofmt -w internal/codex`; run `go test ./internal/codex`.
- [ ] Commit: `fix(codex): preserve completed turn metadata`.

### Task 5: Evaluate completed managed turns and create transition plans

**Files:**
- Modify: `internal/cognition/coordinator.go`, `internal/cognition/coordinator_test.go`

**Interfaces:**
- Produces `Observe(event runtime.Event, sessions SessionSnapshot) (*Plan, error)` and `Commit(plan Plan)` / `Fail(plan, error)`.
- `Plan` distinguishes continue guidance, transition dispatch, and pause.

- [ ] Write tests for delta accumulation by agent/turn, exactly-once parsing after terminal event, clearing buffers, manual-turn exclusion, stale turn rejection, continue, each upward/downward transition, and stop-to-paused.
- [ ] Run `go test ./internal/cognition -run 'Observe|Decision'`; expect failure because observation is not implemented.
- [ ] Accumulate only `KindMessage` events whose backend method identifies assistant message content and whose agent/turn matches active QAC work.
- [ ] On completed turn, parse once, create the real `qac.Request`, call the configured policy, and retain `Decision` unchanged in the plan.
- [ ] Return continue guidance without budget changes; return a pending transition with work ID, source, destination, and source turn; return pause for QAC stop.
- [ ] Reject additional requests while a transition is pending.
- [ ] Run `gofmt -w internal/cognition`; run `go test ./internal/cognition`.
- [ ] Commit: `feat(cognition): evaluate managed turn requests`.

### Task 6: Commit successful handoffs through the app

**Files:**
- Modify: `internal/app/app.go`, `internal/app/update.go`, `internal/app/app_test.go`
- Modify: `internal/runtime/fake/fake.go`

**Interfaces:**
- App owns `*cognition.Coordinator` and handles `transitionResultMsg`.
- `transitionResultMsg` contains the plan identity and dispatch error.

- [ ] Add fake-session support for a controlled `Send` error and unique turn IDs.
- [ ] Write app tests for global task starts Wraith, success transfers ownership to Shade without closing Wraith, failed destination send retains source and budget, global steering follows new owner, and direct steering bypasses QAC.
- [ ] Run `go test ./internal/app -run 'QAC|Global'`; expect failure because the app has no coordinator.
- [ ] Construct the coordinator only for enabled valid QAC config after sessions start. Route global steering to `StartWork` or the active owner; retain old global event-only behaviour when disabled.
- [ ] On each session event, update UI first, call coordinator observation second, and dispatch resulting input through a Bubble Tea command.
- [ ] Check destination availability again immediately before `Send`. On result, call `Commit` only after `Send` succeeds; call `Fail` otherwise. Never close the source session.
- [ ] Run `gofmt -w internal/app internal/runtime/fake`; run `go test ./internal/app ./internal/runtime/fake`.
- [ ] Commit: `feat(app): route work through qac handoffs`.

### Task 7: Render structured QAC state and events

**Files:**
- Modify: `internal/event/event.go`, `internal/app/update.go`, `internal/ui/dashboard/model.go`, `internal/ui/dashboard/view.go`, `internal/ui/agentdetail/model.go`, `internal/app/app_test.go`
- Create: `internal/ui/dashboard/model_test.go`

**Interfaces:**
- Produces `event.KindQAC` and QAC event metadata fields.
- Dashboard receives enabled state and active owner from app state.

- [ ] Add view tests that require `QAC OFF` when disabled, `QAC ON / WRAITH` when active, and a compact `◆ QAC WRAITH → SHADE ESCALATE 0.63 / 0.55` event.
- [ ] Run focused UI tests; expect failure because QAC is hard-coded off and has no event kind.
- [ ] Add QAC event kind and a compact formatter sourced from the stored QAC decision; do not recompute score or factors.
- [ ] Replace static dashboard/detail QAC labels with model state. Keep bright red for the transition only and preserve narrow-terminal limits.
- [ ] Run `gofmt -w internal/event internal/app internal/ui`; run `go test ./internal/ui/... ./internal/app`.
- [ ] Commit: `feat(ui): show qac ownership and transitions`.

### Task 8: Add full fake-runtime coverage and complete local verification

**Files:**
- Modify: `internal/app/app_test.go`, `ghost.toml.example`

- [ ] Add an end-to-end fake test: global work starts Wraith, Wraith request moves to Shade, Shade request moves to Veil, Veil release returns to Shade, and all previous sessions remain open.
- [ ] Add a QAC event assertion with action, source, destination, score, threshold, and work ID.
- [ ] Add example configuration comments for QAC activation/cooldown semantics and the JSON protocol.
- [ ] Run `make fmt`, `make test`, `make vet`, `make build`, and `go test -race ./...`.
- [ ] Commit: `test(app): cover qac ownership cycle`.

### Task 9: Publish QAC and remove local development wiring

**Files:**
- Modify: `go.mod`, `go.sum`, `.github/workflows/ci.yml` if present

- [ ] Verify QAC is clean and passes `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...`.
- [ ] Create and push the annotated `v0.1.0` QAC tag.
- [ ] Replace the local module override with `go get github.com/haha-systems/qac@v0.1.0`, then run `go mod tidy`.
- [ ] Add CI command `! grep -q '^replace github.com/haha-systems/qac' go.mod`.
- [ ] Run `go list -m github.com/haha-systems/qac`; require `v0.1.0` rather than a local path.
- [ ] Run `make test`, `make vet`, `make build`, and `go test -race ./...`.
- [ ] Commit: `build: use qac v0.1.0`.
