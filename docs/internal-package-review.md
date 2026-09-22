# Internal package review

Date: 2026-09-22

## Scope

This review covers every Go package below `internal/`: 21 package paths and about 11,800 production lines. It prioritizes correctness, clear ownership, readable control flow, and normal Go practices.

The review used direct source and caller inspection, relevant tests, a Gortex context query, `go vet`, `gofmt`, and package coverage. Terra agents reviewed the control, state, runtime, and integration packages. A Luna agent reviewed the UI and other leaf packages. The primary review checked their findings against the source.

Implementation is tracked as small jj slices. The pre-existing coordinator refactor was preserved as its own slice.

## Priority guide

- High: fix first because the issue can produce incorrect state, cross-session data, ignored cancellation, a panic, or a broken user view.
- Medium: fix after the high-priority items because the issue weakens validation, makes valid configuration fail later, or makes control flow hard to maintain.
- Low: useful cleanup with limited immediate risk.

## Progress kanban

| Done | In progress | Next |
| --- | --- | --- |
| Terminal reason, replay history, CES completion gate, notification routing, startup deadline, sidebar values, fake-session close race, entry-resource hierarchy, nested trace paths, dashboard selection clamp, artifact QAC validation, terminal-cell widths, pane sync, frame activation, CES alias collection, typed protocol turn keys, pending-dispatch ownership, usable owner sessions, reasoning effort delivery, event application helpers, lifecycle handlers, session event handler | — | Remaining Update routing, controller interface, unsupported MCP/hooks policy |

## High-priority findings

### 1. Use the valid terminal reason

[`internal/cognition/orchestrator.go:60`](../internal/cognition/orchestrator.go#L60) passes `underdetermined; leading hypothesis is not confirmed` to `SetTerminal`. The epistemic store accepts only `underdetermined`. The write fails and the application can report `phase_execution_failed` instead of the real outcome.

Suggested change:

- Store `underdetermined` as the stable reason code.
- Put the longer explanation in trace detail or presentation text.
- Keep the current test assertion for the exact status and reason.

This defect currently fails `TestOrchestratorFollowsForwardFlowAndStopsUnderdeterminedWork`.

### 2. Restore event history during replay

[`internal/epistemic/store.go:72`](../internal/epistemic/store.go#L72) applies replayed events to state but does not restore `store.events`. `Events()` then returns an empty stream, and a later append starts sequence numbering at one.

Suggested change:

- Append a defensive copy of each event after it passes sequence and apply validation.
- Test that replay preserves the event stream.
- Commit once after replay and assert that the next sequence is monotonic.

### 3. Keep completion under CES control

[`internal/app/ces_controller.go:58`](../internal/app/ces_controller.go#L58) converts a model-provided `completion_recommended` flag into a confirmed leading hypothesis. The orchestrator can then complete the work. This makes an advisory model field act as a completion decision.

Suggested change:

- Record the model flag as advisory evidence only.
- Add a CES-owned completion gate that checks recorded verification evidence, frame completion conditions, and residual uncertainty.
- Change the fixture at [`internal/app/ces_test.go:158`](../internal/app/ces_test.go#L158) so the model flag alone cannot complete work.

This keeps the documented boundary: CES selects the next cognitive operation, QAC selects the resource, and the runtime executes it.

### 4. Do not broadcast addressed notifications

[`internal/codex/client.go:190`](../internal/codex/client.go#L190) broadcasts a notification to every live session when its non-empty thread ID has no route. A stale notification after unsubscribe can therefore appear in another agent's stream.

Suggested change:

- Broadcast only notifications that contain no thread ID.
- Drop or record an addressed notification when its route is absent.
- Extend `TestUnsubscribeStopsDelivery` with a stale addressed notification.

### 5. Respect the caller deadline when a session starts

[`internal/codex/runtime.go:96`](../internal/codex/runtime.go#L96) calls `newSession` with the runtime background context. `thread/start` therefore ignores the deadline passed to `Runtime.Start`, including the phase deadline supplied at [`internal/cognition/runner.go:132`](../internal/cognition/runner.go#L132).

Suggested change:

- Use the caller context for `startThread`.
- Use the runtime context only for the long-lived listener.
- Add a blocked `thread/start` test that must stop at the caller deadline.

### 6. Preserve important sidebar values

[`internal/ui/chrome/chrome.go:119`](../internal/ui/chrome/chrome.go#L119) reserves a fixed nine-cell label gutter and truncates every value to the remainder. Dashboard goals, phase routes, and terminal reasons disappear at supported widths.

Suggested change:

- Let long values wrap onto full-width continuation rows, or add a row type that supports continuation lines.
- Keep labels aligned on the first row.
- Add direct width-20 chrome tests and retain the dashboard regression tests.

Five dashboard tests currently fail because these values are truncated.

### 7. Remove the fake-session close race

[`internal/runtime/fake/fake.go:95`](../internal/runtime/fake/fake.go#L95) checks `closed`, unlocks, and then sends. `Close` can close the output channel between the check and send, causing `send on closed channel`.

Suggested change:

- Coordinate `Emit` and `Close` through one lock or one channel owner.
- Add a race test that emits while closing.
- Make buffer exhaustion visible instead of silently dropping test events.

## Medium-priority findings

### Application structure

- [`internal/app/update.go:23`](../internal/app/update.go#L23): `Model.Update` handles session lifecycle, CES events, navigation, input, dispatch, shutdown, and view updates. Extract message-specific handlers and leave `Update` as the router.
- [`internal/app/ces_controller.go:32`](../internal/app/ces_controller.go#L32): seven function fields make controller dependencies difficult to follow and allow missing hooks. Replace them with a narrow interface and explicit production and test implementations.
- [`internal/app/ces.go:398`](../internal/app/ces.go#L398): `cesAliases` repeats collection and sorting for each object kind. Use one ID collection helper and a table of prefixes.

### Cognition validation and coordination

- [`internal/cognition/artifact.go:256`](../internal/cognition/artifact.go#L256): phase artifacts do not validate `qac_request`, although direct QAC parsing validates direction, score ranges, and failed attempts. Extract shared QAC validation and call it from both paths.
- [`internal/cognition/coordinator.go:99`](../internal/cognition/coordinator.go#L99): replace the hidden `agent + "\x00" + turn` map key with a `turnKey` struct and use `map[turnKey]struct{}` for membership.
- [`internal/cognition/coordinator.go:135`](../internal/cognition/coordinator.go#L135): detect `turn/started`, `turn/completed`, and `turn/failed` from protocol metadata rather than a display summary. Share these protocol helpers with the phase runner.
- [`internal/cognition/coordinator.go:126`](../internal/cognition/coordinator.go#L126): `Fail` clears `awaitAgent` without confirming that the failed plan owns the current expectation. Replace `pending` and `awaitAgent` with one pending-dispatch value containing the plan and agent IDs.
- [`internal/cognition/coordinator.go:369`](../internal/cognition/coordinator.go#L369): the current owner is marked available even when its session is absent or failed. Require a usable session, then document any exception needed for a short running-to-idle state delay.
- [`internal/cognition/coordinator.go:33`](../internal/cognition/coordinator.go#L33): split streamed turn capture from work and allocation state. The current type owns event capture, Jev assessment, QAC allocation, work lifecycle, and budgets.
- [`internal/cognition/artifact.go:197`](../internal/cognition/artifact.go#L197): `Delta` and `Validate` repeat phase-specific traversal. Use per-phase conversion helpers or a small visitor so each phase defines its object and relation mapping once.

### Epistemic state

- [`internal/epistemic/store.go:228`](../internal/epistemic/store.go#L228): each new frame is created as active before `ActiveFrame` and supersession checks run. Create frames as inactive, then activate only the selected frame after validation. Reject missing or ambiguous selection.
- [`internal/epistemic/store.go:88`](../internal/epistemic/store.go#L88): `Commit` mixes validation, ID allocation, object construction, relation resolution, status changes, retractions, append order, and result assembly. Build a validated commit plan first, then append it atomically.
- [`internal/epistemic/projection.go:14`](../internal/epistemic/projection.go#L14): object-kind indexing and per-kind filtering repeat slice plumbing. Add one typed state index helper and keep projection policy separate from storage shape.
- [`internal/epistemic/state.go:8`](../internal/epistemic/state.go#L8): move payload decoding and mutation into event-specific helpers. Keep `applyEvent` as a short dispatcher.

### Configuration and tracing

- [`internal/config/config.go:248`](../internal/config/config.go#L248): validation allows `entry_resource` outside the policy hierarchy. Reject that configuration early.
- [`internal/config/config.go:35`](../internal/config/config.go#L35): `mcp` and `hooks` are parsed but have no runtime consumers. `effort` reaches `SessionConfig` but is not sent by [`internal/codex/runtime.go:129`](../internal/codex/runtime.go#L129). Wire supported options into the App Server request, or reject non-default unsupported values.
- [`internal/trace/trace.go:33`](../internal/trace/trace.go#L33): `Open` fails for a valid nested path when its parent does not exist. Create the parent directory with owner-only permissions before opening the trace file.

### Codex session lifecycle

- [`internal/codex/session.go:89`](../internal/codex/session.go#L89): a `turn/started` notification can replace the local turn ID before `turn/start` returns an error. Cleanup then uses the obsolete ID and can leave the guard running. Complete the currently active turn, or make replacement and failed-call cleanup one operation.
- [`internal/codex/session.go`](../internal/codex/session.go): separate session lifecycle, notification normalization, rate-limit parsing, and display formatting. The current 575-line file makes protocol changes harder to review.

### UI state and terminal width

- [`internal/ui/dashboard/model.go:294`](../internal/ui/dashboard/model.go#L294): clamp `selected` when `SetAgents` replaces the roster. Otherwise a smaller roster can leave an invalid selection.
- [`internal/ui/dashboard/model.go:497`](../internal/ui/dashboard/model.go#L497): use terminal-cell width instead of rune count for event prefixes. Add CJK and emoji tests.
- [`internal/ui/layout/layout.go:214`](../internal/ui/layout/layout.go#L214): `SteeringRows` also counts runes instead of terminal cells. Use the same width rules as rendering.
- [`internal/ui/pane/pane.go:47`](../internal/ui/pane/pane.go#L47): call `sync` after size and content changes. A viewport clamped to the bottom can otherwise remain marked as detached and count false new entries.

## Package-by-package coverage

| Package | Result |
| --- | --- |
| `internal/app` | High completion-boundary issue; split update routing and controller dependencies. |
| `internal/codex` | High notification routing and startup-context issues; make failed-turn cleanup atomic. |
| `internal/cognition` | High terminal-reason defect; validate artifact QAC requests and separate coordinator concerns. |
| `internal/config` | Require the entry resource in the hierarchy; reject or implement unused options. |
| `internal/epistemic` | High replay defect; make frame activation explicit and split commit planning from append. |
| `internal/event` | No material change suggested. The package is a small data contract. |
| `internal/history` | No material change suggested. Existing focused coverage is strong. |
| `internal/logging` | No material structural change suggested. Add tests only when behavior changes. |
| `internal/model` | No material change suggested. The package contains small presentation types and fixtures. |
| `internal/runtime` | No material core-contract change suggested. Keep guard behavior focused and tested. |
| `internal/runtime/fake` | Fix the emit/close race and make dropped events explicit. |
| `internal/trace` | Create secure parent directories for nested trace paths. |
| `internal/ui/agentdetail` | No material change suggested. |
| `internal/ui/chrome` | High sidebar truncation issue; add direct narrow-width tests. |
| `internal/ui/dashboard` | Clamp selection and use terminal-cell width. |
| `internal/ui/epistemic` | No material change suggested. |
| `internal/ui/footer` | No material change suggested. |
| `internal/ui/keymap` | No material change suggested. |
| `internal/ui/layout` | Use terminal-cell width for steering row calculations. |
| `internal/ui/pane` | Recompute follow state after resize and content replacement. |
| `internal/ui/theme` | No material change suggested. |

## Verification state

- `GOCACHE=/tmp/ghost-gocache go vet ./internal/...`: passed after the implemented slices.
- `gofmt -l internal`: passed with no output.
- `GOCACHE=/tmp/ghost-gocache go test -count=1 ./internal/...`: passed after the implemented slices.
- The focused fake-session race test also passed under `go test -race`.
- Coverage should be regenerated after the remaining control-flow refactors.

## Suggested implementation order

1. Fix the invalid terminal reason and the sidebar rendering so the existing suite is green.
2. Fix replay history, completion ownership, notification routing, and startup cancellation with new failing regression tests first.
3. Fix frame activation, QAC validation, entry-resource validation, and fake-session concurrency.
4. Split the large control functions only after their behavior is protected by focused tests.
5. Apply the smaller width, selection, trace-path, and duplication cleanups.
