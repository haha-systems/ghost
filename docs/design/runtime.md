# Runtime contracts

Status: Milestone C, Step 2 connects the controller to the communication router and runtime sessions. Step 3 restart recovery is not done. See [implementation status](../IMPLEMENTATION-STATUS.md) for the fake-runtime acceptance results.

## Agents and sessions

`run.Controller` builds one in-memory `core.Agent` for each configured agent. It keeps the generated Ghost agent ID separate from the provider session ID. `WorkspaceSnapshot()` returns detached agent, task, message, and event slices. Session state sets availability and activity. These records are not restored after restart.

`StartSessions` adds each agent's communication capability brief to the session instructions and records a visible core event. `SetSessionsWithFailures` installs sessions and reports startup failures to the router. Missing sessions stay offline; failed sessions cause pending deliveries to fail with a reason.

The projection uses the existing `config.AgentSet` and `runtime.Session` interfaces. Codex is the only configured production provider. Step 2's delivery path uses the existing `Send`, `Steer`, and `Interrupt` methods. It does not verify behavior with a live provider. See [`BackendConfig`](../../internal/config/config.go), [`run.owner`](../../internal/run/controller.go), and [`runtime.Session`](../../internal/runtime/runtime.go).

## Tasks and lifecycle

`Start(goal)` creates a root task with requester `you`. `Send(recipients, text)` creates or reuses a task, records one router message, and routes it only to the resolved recipients. An idle session gets `Send`; a running session gets `Steer`. The delivered text carries a structured message frame.

`Cancel()` cancels the active run and its open descendants. `CancelTask(id)` cancels the named task and open descendants. The controller interrupts active owners through `runtime.Session.Interrupt`. The TUI accepts an exact task ID or unique prefix.

`StartWithPolicy(goal, policy)` sets the effective CES policy for one root task. A delegated child inherits its parent's CES policy under the router's rule. Step 2 records this policy, but child execution uses the standard session path; it does not run a separate CES orchestration for the child.

`epistemic.Task` remains the authority for CES evidence and work status. `cognition.Orchestrator.Advance` chooses the next phase, QAC chooses a resource, and the runtime executes that phase. QAC and CES are separate systems. A request to enable CES without configured cognition reports effective policy `off` with reason `unsupported: QAC not configured`.

`SetCESPolicy(on|off)` changes the workspace default. A change applies at once when no provider turn or CES phase is active. During active work, the requested policy is pending and takes effect at the next `Start`. It does not interrupt the phase or clear epistemic state. `Status` reports requested, effective, and pending policy. A task override does not change the workspace default.

## Broker, events, and projections

Each controller owns one `comms.Router`. The router is the source of truth for messages, delivery states, and delegated tasks. The workspace projection copies router snapshots for readers; it does not create another message record. Router events pass once through the controller's core event path and the same JSONL event log as runtime events.

The controller owner accepts work on its request queue. One broker worker serializes router operations and calls runtime sessions outside the owner loop. Router delivery callbacks run after the router releases its mutex. The worker calls the owner only after the router operation returns. This avoids holding router state during a provider call and keeps provider calls off the controller owner.

`internal/run` adapts `runtime.Event`, legacy `event.Event`, cognition phase events, and structured QAC plans to `core.Event`. It maps IDs and dotted event types and keeps source data as JSON payload. Raw provider telemetry remains an event; only validated channel requests become broker messages or tasks.

The default workspace retains one router message ID for a broadcast and a delivery result per recipient. `WorkspaceSnapshot`, `MessagesForAgent`, `AllMessages`, `TasksForAgent`, and `Events` read detached projections of the same broker records.

## Recovery and limits

Runtime task, message, session, and router state is in memory. The JSONL event log records broker events, but event replay does not rebuild that state. Restart recovery and retry after restart are Step 3 work.

`ghost serve` keeps its current RPC methods. It does not expose the new workspace snapshot or policy methods. See [headless and runtimes](headless-and-runtimes.md).
