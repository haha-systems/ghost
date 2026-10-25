# Runtime contracts

Status: Milestone B step 1 is implemented and tested. The TUI in step 2 is not
started.

## Agents

`run.Controller` builds one in-memory `core.Agent` for each configured agent.
It keeps a generated Ghost agent ID separate from the provider session ID.
`WorkspaceSnapshot()` returns detached agent, task, message, and event slices.
Live session state sets agent availability and activity. These records are not
restored after restart.

The projection uses the existing `config.AgentSet` and `runtime.Session`
interfaces. Codex is the only configured production provider. It does not add
provider capability discovery or permission enforcement. See
[`BackendConfig`](../../internal/config/config.go),
[`run.owner`](../../internal/run/controller.go), and
[`runtime.Session`](../../internal/runtime/runtime.go).

## Tasks and lifecycle

`Start(goal)` creates a task with requester `you`. Its owner and participants
come from the agents given the work. A task moves from queued to running when
the controller dispatches a CES phase or receives a provider turn-start event.
Controller and provider results set completed, failed, or cancelled status.
The same task record feeds all task queries.

`StartWithPolicy(goal, policy)` sets a policy for one task. A delegated child
task must inherit its parent's CES policy unless the caller gives an explicit
override. Delegation is not implemented in this step, so this rule is a
contract for the later delegation work.

`epistemic.Task` remains the authority for CES evidence and work status.
`cognition.Orchestrator.Advance` chooses the next phase, QAC chooses a resource,
and the runtime executes that phase. QAC and CES are separate systems. The
existing default remains CES on when valid QAC configuration is present. A
request to enable CES without configured cognition reports effective policy
`off` with reason `unsupported: QAC not configured`.

`SetCESPolicy(on|off)` changes the workspace default. A change applies at once
when no provider turn or CES phase is active. During active work, the requested
policy is pending and takes effect at the next `Start`. It does not interrupt
the phase or clear epistemic state. `Status` reports requested, effective, and
pending policy. A task override does not change the workspace default.

## Runtime adapter

`internal/run` adapts `runtime.Event`, legacy `event.Event`, cognition phase
events, and structured QAC plans to `core.Event`. It maps IDs and dotted event
types, keeps the source data as JSON payload, and derives tool summaries from
structured provider fields. The core package remains a leaf.

`Send(recipients, text)` stores a human message and sends it only to resolved
recipients. It reads `@agent` and `@all` mentions. If no task is active, it
starts one with the message text. Existing `Start`, `Steer`, and `Cancel` calls
remain available. `MessagesForAgent`, `AllMessages`, `TasksForAgent`, and
`Events` provide shared projections; tabs do not keep separate copies.

The controller uses its owner goroutine for state changes. The workspace store
uses a lock for detached concurrent reads. Runtime task and message state is
in memory only; event replay does not rebuild it yet.
