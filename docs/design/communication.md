# Communication and events

Status: Runtime message and event projections are implemented in milestone B
step 1. The TUI and replay reducer remain open.

## Messages

`run.Controller` stores human input and agent replies as `core.Message` values.
Each message has a task and thread ID. Replies point to the latest human
message. `Send(recipients, text)` resolves configured agent names and `@`
mentions to agent IDs. `@all` creates a broadcast. Unknown names return an
error. Active runs steer only the resolved persistent sessions; an idle
controller starts a task with the message text.

The message store is shared. `MessagesForAgent(id)` returns messages sent by,
addressed to, or broadcast to that agent. `AllMessages()` returns the same
records without per-tab copies. Delivery status is recorded in the core
contract, but this step does not add retry or delivery recovery.

## Event envelope

`core.Event` carries ID, schema version, time, workspace, actor, source, type,
category, optional task/session/agent/thread IDs, correlation and causation
IDs, per-stream sequence, and JSON payload. The runtime adapter maps provider,
legacy, CES phase, and QAC plan data into dotted event types such as
`conversation.agent_message`, `tool.call`, `session.state`, `provider.error`,
`usage.tokens`, `decision.qac`, `ces.phase`, `task.status_changed`, and
`ces.policy_changed`.

`CategoryOf` maps prefixes to conversation, task, tool, coordination, memory,
or decision. `WorkspaceSnapshot()` and `Events(filter)` return detached copies.
Event filters support agent, category, type, and payload search.

## Durable log and recovery

The controller appends core events to
`<workdir>/.ghost/events/<session-start-id>.jsonl`. The `events` directory is
mode `0700`; new log files are mode `0600`. The TUI uses the configured
workspace root. If agent roots differ, the first agent by name selects the TUI
log root. `ghost serve` selects the requested configured workspace before a
run. Each log is new, so existing files are not rewritten. A write error is
visible in controller status and does not stop the run.

`core.EventLog` restores stream counters when it opens a file and can replay
valid records. This step does not rebuild agents, tasks, messages, or provider
sessions from the log. It does not retry actions after restart. The existing
trace remains a diagnostic log, separate from the event log. See
[`trace.Writer`](../../internal/trace/trace.go) and the
[headless event and resume design](headless-and-runtimes.md), section 2.
