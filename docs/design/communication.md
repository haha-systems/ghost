# Communication and events

Status: Contracts implemented and tested; runtime delivery and replay are not
wired.

## Message

Status: Implemented as a contract; delivery is not started.

`core.Message` is a communication record with a sender, recipients,
thread, optional task and reply references, type, content, creation time, and
delivery status. Types are `direct`, `broadcast`, `request`, `reply`,
`task_delegation`, `task_result`, and `system`. Broadcast uses no explicit
recipients or the `*` wildcard. Direct and request messages require recipients.
Replies require `ReplyTo` and recipients; task delegation and task result
messages require a task ID and recipients.

Delivery status values are `pending`, `delivered`, `read`, and `failed`. The
message ID and content should remain stable when delivery state changes. A
future runtime event records each delivery-state change. No runtime routing or
message store exists yet.

Threads collect related conversation. A task can point to a thread, and a
message can point to both a thread and a task. `@agent_id` and `@all` are
presentation syntax only; the runtime must resolve them to known, permitted
agent IDs before delivery. Unknown or forbidden targets must fail visibly.

## Event envelope

Status: Implemented in `internal/core`.

`core.Event` carries ID, schema version, time, workspace, actor, source, type,
category, optional task/session/agent/thread IDs, correlation and causation
IDs, per-stream sequence, and JSON payload. `CategoryOf` maps event type
prefixes to conversation, task, tool, coordination, memory, or decision.
Unknown prefixes fail validation.

`NewID` makes time-ordered IDs with a prefix and random suffix. `Sequencer`
assigns increasing sequence numbers for each stream. Stream selection uses the
task ID first, then the session ID, then the workspace. Sequence order is the
order authority; timestamps are for display. Correlation groups related work;
causation points to the event that caused a new event.

## Storage and recovery

Status: Local event log implemented; runtime state recovery is designed only.

Use a local append-only JSONL event log; do not add a broker. `core.EventLog`
assigns sequence numbers, appends one JSON event per line, and can sync each
append. On open, it restores per-stream sequence state. It skips and truncates
one corrupt final line and reports the skipped count. Corruption before the
final line is an error. `Replay` reads valid records in file order.

The log is the durable event source. Runtime projections will rebuild from
events and must be safe to replay. Recovery must not repeat an action whose
completion is uncertain: mark that task interrupted and require a new control
decision. The log does not yet rebuild runtime agents, tasks, or provider
sessions; that reducer and restart path are not started.

The existing trace remains a diagnostic log. It is not this event store. See
[`trace.Writer`](../../internal/trace/trace.go) and the existing
[headless event and resume design](headless-and-runtimes.md), section 2.
