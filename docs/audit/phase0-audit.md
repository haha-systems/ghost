# Phase 0 audit

Status: Implemented. This records source behavior found in this checkout on
2026-10-25 and separates existing runtime behavior from the new Phase A
contracts. “Not present” means no executable implementation was found.

## 1. Agents and sessions

`config.AgentSet` is a map from a configured name to `BackendConfig`, which
stores runtime, model, effort, working directory, and prompt settings. It is
not a durable agent record. `app.configuredAgents` maps it to display rows in
`model.Agent`. `run.owner` holds active `runtime.Session` values in a map keyed
by configured name; a session is a live provider handle, not an agent record.
QAC maps resources to configured agents in `cognition.Coordinator`. See
[`config.AgentSet` and `BackendConfig`](../../internal/config/config.go),
[`configuredAgents` and `app.Model`](../../internal/app/app.go),
[`run.owner`](../../internal/run/controller.go),
[`runtime.Session`](../../internal/runtime/runtime.go), and
[`Coordinator`](../../internal/cognition/coordinator.go).

The local, untracked (gitignored) `.ghost/agents/GHOST.md` and the
`shade`, `veil`, and `wraith` `SOUL.md` files are instruction
files. Their headings and prose describe operating rules and roles; they are
not machine-readable agent or session records.

## 2. Codex sessions and other providers

`codex.Runtime.Start` starts the Codex app-server process, calls `initialize`,
and creates a thread with `startThread`. `rpcClient.call` and `notify` exchange
JSON-RPC messages over standard input and output. A `codex.Session` binds a
configured agent to one provider thread and routes notifications by thread.
`Session.Send`, `Steer`, and `Interrupt` use `turn/start`, `turn/steer`, and
`turn/interrupt`.

The shared `runtime.Runtime` interface can accept other implementations, but
`config.validate` accepts only `codex`; it explicitly rejects `claude` and
`openai-compat` as not implemented. Claude and other provider adapters are not
present. `RuntimeClaude` and `RuntimeOpenAICompatible` in `internal/core` are
contract values only. See [`Runtime.Start` and `startThread`](../../internal/codex/runtime.go),
[`rpcClient`](../../internal/codex/client.go),
[`Session` lifecycle](../../internal/codex/session_lifecycle.go),
[`Runtime`](../../internal/runtime/runtime.go), and
[`validate`](../../internal/config/config.go).

## 3. Native OpenAI-compatible agents

Not present as executable code. `config.validate` rejects `openai-compat`; the
repository has no OpenAI-compatible runtime or request loop. The new
`RuntimeOpenAICompatible` value in `core` is only a contract label. The proposed
adapter is described in the existing
[headless and runtimes design](../design/headless-and-runtimes.md).

## 4. Provider event normalisation

`codex.normalizeNotificationPayload` maps Codex item types and notification
methods to `runtime.EventKind` values. `notificationSummary` extracts display
text, and `rateLimitMetadata` converts rate-limit fields to metadata. The
Codex session calls this normaliser before emitting. `app.dashboardEventKind`
maps the shared runtime kinds into the presentation `event.Kind`; `handleSessionEvent`
then builds the event used by history and views. This is a Codex normaliser,
not a provider-neutral adapter contract. See
[`normalizeNotificationPayload` and `normalizeKind`](../../internal/codex/notification.go),
[`notificationSummary` and `rateLimitMetadata`](../../internal/codex/notification_details.go),
[`Session.listen` and `Session.emit`](../../internal/codex/session.go),
[`dashboardEventKind` and `handleSessionEvent`](../../internal/app/update.go),
[`runtime.Event`](../../internal/runtime/runtime.go), and
[`event.Event`](../../internal/event/event.go).

## 5. Event and state storage

`trace.Open` opens an append-only file. `trace.Writer.Write` uses JSON encoding,
so each record is one JSONL line. Normal traces keep summaries and raw-payload
digests; verbose traces can store raw provider payloads. Trace records do not
have a stable event ID, schema version, or sequence number.

`epistemic.Store` keeps ordered CES events and a materialised state in memory.
`Store.append` assigns store sequence numbers; `Store.Events` copies the event
list; `epistemic.Replay` rebuilds a store from that list. The store has no disk
writer. `history.Store` keeps a bounded in-memory projection of run events and
joins response fragments. `serve.Server.publish` sequences events in an
in-memory journal. `run.traceCES` writes summary metadata, not complete
`epistemic.Store.Events` payloads. Neither history nor the serve journal is
durable. See
[`trace.Open` and `Writer.Write`](../../internal/trace/trace.go),
[`Store`, `append`, `Events`, and `Replay`](../../internal/epistemic/store.go),
[`history.Store`](../../internal/history/store.go),
[`Server.publish`](../../internal/serve/server.go), and
[`run.traceCES`](../../internal/run/ces_trace.go).

Phase A adds `core.Event` and `core.EventLog`: versioned JSONL records,
per-stream sequence restoration, optional sync, and final-line recovery. They
are a new durable event contract, not a state reducer, and no existing runtime
path calls them. See [`core.Event`](../../internal/core/event.go) and
[`core.Open`, `Append`, and `Replay`](../../internal/core/log.go).

## 6. Steering and interruption

`runtime.Session` exposes `Steer` and `Interrupt`; the Codex implementation
sends the matching JSON-RPC calls. The TUI's agent-detail handlers call those
session methods directly. Global input calls `run.Controller.Steer`. During
CES, the controller records the input but does not deliver it to the active
phase session. If configured cognition has no active work, it starts new CES
work; with active non-CES work, it sends input to the persistent owner session.
`run.Controller.Cancel` cancels the phase context and marks active CES work
incomplete. The headless server exposes `run.steer` and `run.cancel`; it does
not expose `run.interrupt`. Its `delivery` label reflects CES activity, not
confirmed provider delivery: it can say `live` while CES only records the input.
See
[`Session.Steer` and `Session.Interrupt`](../../internal/codex/session_lifecycle.go),
[`handleAgentSteering`, `handleAgentInterrupt`, and `handleGlobalSteering`](../../internal/app/update.go),
[`Controller.Steer` and `Controller.Cancel`](../../internal/run/controller.go),
and [`Server.call`, `steer`, and `cancel`](../../internal/serve/server.go).

## 7. QAC and Jev

`Coordinator.AllocateWithSituation` passes a bounded request to the configured
QAC policy, which selects a resource under capability, availability, hierarchy,
and budget rules. Optional `JevClient` assessment is gated by
`shouldAssessJev`: repeated EXECUTE attempts and verified, structured low
progress. `ParseJevJudgment` rejects malformed or out-of-range output.
`applyJevAdvice` changes only advisory expected-gain and uncertainty inputs;
QAC still selects the resource. A live Jev service is not configured by the
default coordinator. See [`Coordinator.New`, `AllocateWithSituation`, and
`assessJev`](../../internal/cognition/coordinator.go),
[`ProgressVerifier`, `JevClient`, and `ParseJevJudgment`](../../internal/cognition/jev.go),
and [`shouldAssessJev` and `applyJevAdvice`](../../internal/cognition/jev.go).

## 8. CES execution path

`run.owner.startCESWork` creates an `epistemic.Store`, asks the coordinator for
an initial resource, and creates a `cognition.Orchestrator`. `run.owner.runPhase`
freezes a phase projection and starts a worker. `PhaseRunner.Run` opens a fresh
runtime session, sends the phase request, and parses the result. The owner
validates and commits the artifact; `cesController.process` then calls
`Orchestrator.Advance` to choose the next phase and the coordinator to choose
the resource. `finishCESWork` records terminal state. CES state is in memory;
the trace is diagnostic output, not a replayable CES store. See
[`startCESWork` and `runPhase`](../../internal/run/ces.go),
[`PhaseRunner.Run`](../../internal/cognition/runner.go),
[`cesController.process`](../../internal/run/ces_controller.go),
[`Orchestrator.Advance`](../../internal/cognition/orchestrator.go), and
[`epistemic.Store`](../../internal/epistemic/store.go).

For CES concepts and the division between phase choice, resource choice, and
execution, see [CES](../CES.md).

## 9. First-class tasks and messages

A CES `epistemic.Task` and a QAC `cognition.WorkItem` exist, but they represent
CES work and allocation state. They are not a general multi-agent task queue,
and their stores are in memory. Phase A adds `core.Task`, `core.Message`, and
`core.EventLog` contracts; they are not wired to runtime state or delivery. A
persisted agent-to-agent message store is not present. `history.Entry` is an
event projection, not a message record. See
[`epistemic.Task`](../../internal/epistemic/types.go),
[`cognition.WorkItem`](../../internal/cognition/work.go),
[`core.Task`](../../internal/core/task.go),
[`core.Message`](../../internal/core/message.go),
[`core.EventLog`](../../internal/core/log.go),
[`history.Entry`](../../internal/history/entry.go), and
[`history.Store`](../../internal/history/store.go).

## 10. TUI coupling

`app.Model` holds `runtime.Session` handles and `run.Controller`. `app.Init`
starts sessions; `waitSessionEvent` reads provider-normalised runtime events;
`handleSessionEvent` reads session state and maps runtime kinds for display.
Agent steering and interruption also call session methods from `internal/app`.
The lower `internal/ui/*` models consume presentation data: `dashboard.Model`
uses agent rows and `event.Event`, `agentdetail.Model` uses agent and log rows,
and the CES UI model uses a snapshot and `event.Event`. The TUI is therefore
separated at the view package, but the app layer is coupled to runtime/session
APIs. See [`app.Model`, `Init`, and `waitSessionEvent`](../../internal/app/app.go),
[`handleSessionEvent` and `dashboardEventKind`](../../internal/app/update.go),
[`dashboard.Model`](../../internal/ui/dashboard/model.go),
[`agentdetail.Model`](../../internal/ui/agentdetail/model.go), and
[`internal/ui/epistemic.Model`](../../internal/ui/epistemic/model.go).

## Gaps and risks

- Agent identity is configuration-keyed, while sessions are live handles; the
  new [`core.Agent`](../../internal/core/agent.go) contract is not yet wired to
  a registry. `run.Controller.StartSessions` supports only the Codex runtime.
- Runtime event streams have bounded buffers that may drop events:
  `rpcClient.deliver` and `Session.emit` count drops but do not provide durable
  replay. The new core log is not wired into them.
- CES state and TUI history are in memory. A process restart cannot restore
  active work from the trace alone.
- Active CES steering is recorded but not delivered to the phase session.
- There is no runtime task/message store or `@` address resolver. CES has
  phase-based object permissions in
  [`epistemic/permissions.go`](../../internal/epistemic/permissions.go), but no
  runtime-agent or task `PermissionPolicy` is connected. `runtime.SessionConfig`
  has sandbox and approval modes, but not the new policy.
- Provider event normalization, runtime selection, and TUI session handling
  still have Codex-shaped assumptions. A new adapter would need extra provider
  behavior that is not encoded in `runtime.Event` today.

Sources: [`run.Controller.StartSessions`](../../internal/run/runtime.go),
[`rpcClient.deliver`](../../internal/codex/client.go),
[`Session.emit`](../../internal/codex/session.go),
[`SessionConfig`](../../internal/runtime/runtime.go), and
[`trace.Writer.Write`](../../internal/trace/trace.go).
