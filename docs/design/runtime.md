# Runtime contracts

Status: Designed. The contracts in [`internal/core`](../../internal/core/doc.go)
are implemented and tested; no runtime mapping is wired yet.

## Agent

Status: Implemented in `internal/core`; mapping into `run` is not started.

`core.Agent` is a stable runtime record. It has a typed ID, display name,
runtime and provider kinds, optional provider session binding, availability,
activity, capabilities, permission policy, assigned task IDs, and active task
IDs. `Agent.Validate` checks required identity, enum values, references,
duplicates, and policy lists. It does not start a session or grant a
permission.

The current `config.AgentSet` is configuration, and `run.owner.sessions` is a
set of live `runtime.Session` handles. Future code must map those into
`core.Agent` records without using provider session IDs as agent IDs. See
[`BackendConfig`](../../internal/config/config.go),
[`run.owner`](../../internal/run/controller.go), and
[`runtime.Session`](../../internal/runtime/runtime.go).

## Task and lifecycle

Status: Implemented in `internal/core`; runtime task ownership is designed.

`core.Task` records requester, owner, parent, participants, dependencies,
status, timestamps, CES policy and optional phase, limits, thread, and event
references. `Task.Validate` checks the record. `Task.Transition` is the only
contract-level status change and applies this table:

| From | Allowed states |
| --- | --- |
| queued | running, waiting, failed, cancelled |
| running | waiting, completed, failed, cancelled |
| waiting | running, completed, failed, cancelled |
| completed, failed, cancelled | none |

Starting sets `StartedAt`; entering a terminal state sets `FinishedAt`.
Terminal states cannot change again.

The current `epistemic.Task` is CES state, and `cognition.WorkItem` tracks QAC
ownership. Neither is the new generic task record. A later adapter will relate
them by explicit IDs and events. It must preserve current CES authority:
`Orchestrator.Advance` chooses the phase, QAC chooses the resource, and the
runtime executes it. See [`epistemic.Task`](../../internal/epistemic/types.go),
[`WorkItem`](../../internal/cognition/work.go), and
[`Orchestrator.Advance`](../../internal/cognition/orchestrator.go).

## Adapter boundary

Status: Designed. Adapter wiring is not started.

The runtime owns agent records, task state, routing, policy checks, and
orchestration. An adapter implements provider startup, session calls,
capability reporting, and event conversion. It does not own Ghost task state
or decide the next CES phase. `core` imports no other Ghost package, so later
layers can depend on these contracts without a cycle.

For proposed shared runtime event fields and provider capability checks, see
[headless and runtimes](headless-and-runtimes.md), section 3.
