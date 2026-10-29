# Communication and events

Status: Milestone C, Steps 1–3 are implemented and tested with the fake runtime. The fake runtime covers the Phase 2 acceptance cases listed in `IMPLEMENTATION-STATUS.md`. No live-provider behavior is claimed.

## One source of truth

`internal/comms.Router` owns conversation messages, delivery state, delegated tasks, consultation state, and broker events. `run.Controller` creates one router for its workspace. `WorkspaceSnapshot` copies the router's task and message records for reads; it does not create a second message record. The router's events pass once through the controller's core event store and the same JSONL `EventLog` as runtime events.

The controller owner accepts requests on its request queue. A serial broker worker runs router operations and session delivery. The router releases its mutex before it calls the delivery adapter. The worker calls the owner only after the router operation returns. Provider `Send` and `Steer` calls run on the worker, outside the owner loop. This keeps provider work from holding router state or blocking controller state changes.

The broker and its projections live in memory while Ghost runs. On startup, the controller replays the workspace event history before it starts the broker worker. `Router.Rehydrate` restores messages, recipient delivery states, tasks, parent links, consultations, task message budgets, duplicate delegation keys, and delivery-attempt state. Replay does not call the delivery adapter or start provider sessions.

## Messages and delivery

The sender comes from the authenticated caller or the runtime session that produced the text. The parser does not accept a sender field. The router validates the sender, recipients, task membership, and `CanMessage` permission before it stores one logical message.

A broadcast expands to configured agents other than its sender. It keeps one message ID and records delivery per recipient. Delivery is `pending` while any recipient is pending, `delivered` when all are delivered, and `failed` when no recipient is pending and at least one failed. Failure reasons remain on the message.

An idle recipient session gets `Session.Send`; a running session gets `Session.Steer`. A recipient with no session remains pending. Installing an available session retries pending deliveries. A failed or stopped session fails pending messages with a reason. The TUI shows the status and reason as text.

For human task requests, the delivery worker recalls memory separately for each
recipient before it calls `Send` or `Steer`. Delegated child delivery recalls
for the target agent with the child task and its parent chain. The sanitised
package is prepended to the framed input. These reads run outside the owner
loop and use the configured recall deadline. A failed read leaves the original
frame intact. Agent-to-agent messages do not trigger recall.

Every delivered input has a frame with sender, message ID, task ID, thread ID, type, and content. Delegation frames also include the parent task, objective, expected deliverable, effective limits, permissions, and CES policy. A concise capability brief is added to the session instructions at startup or to its first delivered input. The brief is also recorded as a `coordination.capability_briefed` event.

## Delegation and consultation

`Delegate` creates a queued child task. The requester must own the open parent task. The child records requester, target owner, parent ID, objective, context references, expected deliverable, depth, CES policy, permission policy, resource limits, deadline, and correlation ID. The broker sends a `task_delegation` message to the target. `Complete` and `Fail` close the child and send a `task_result` message to its requester.

The CES rule is to inherit the parent's policy unless an override keeps or strengthens it under `off < observe < enforce`. Permissions are intersected across the parent task, requester, target, and requested child policy. Resource limits can only get tighter. An `ask` mode or `requires_approval` request is rejected; approval is not granted by this broker.

Child tasks use the standard runtime session path. The child policy is stored and inherited, but Step 2 does not start CES orchestration for a child. It records this limit and does not claim a CES child run.

`Consult` sends a question, evidence, and references without creating a child task or changing ownership. `ResolveConsultation` returns a separate opinion with findings and uncertainty. Ghost does not merge the opinion into task facts.

Defaults are delegation depth 3, one visit per agent in a delegation chain, 50 agent messages per task, 2 open consultations per consultant, a 10-minute task timeout, and a 10-minute maximum pending-message age. Duplicate open delegation requests and cycles are rejected. `Cancel` marks an open task and its descendants cancelled, cancels linked consultations, sends result notices, and interrupts active task owners through their existing sessions. `Controller.CancelTask(id)` exposes task cancellation; `Controller.Cancel()` cancels the active run and its children.

## Agent channel protocol

`internal/cognition.ParseAgentChannel` parses at most one strict block in a final provider message. It accepts `ghost-message`, `ghost-delegate`, `ghost-consult`, and `ghost-result` with `schema_version: 1`. It also accepts `ghost-recall`, `ghost-remember`, and `ghost-challenge` with their documented fields and no version field. All blocks reject malformed JSON, duplicate or unknown fields, invalid values, and content above 16 KiB. Blocks inside quoted text or other code fences are ignored. Ordinary text and bare mentions such as `@veil` do not route.

The controller supplies the sender from the session that produced the final text. A claimed sender field is rejected. Parsed requests call `Router.Send`, `Delegate`, `Consult`, or `Complete`/`Fail`. A `ghost-result` is accepted only from the owner of an open child task. A rejection becomes a visible `message.rejected` event and one short notification to the agent for that task, which prevents notification loops. Plain final text is stored as a reply to the task's request; raw provider events remain in the events panel.

| Fence | Required fields | Optional fields |
| --- | --- | --- |
| `ghost-message` | `recipients`, `thread_id`, `type`, `content` | `task_id`, `reply_to` |
| `ghost-delegate` | `target`, `parent_task_id`, `objective`, `context_refs`, `expected_deliverable` | `timeout`, `ces_policy`, `permissions` |
| `ghost-consult` | `target`, `parent_task_id`, `question` | `evidence`, `references` |
| `ghost-result` | `task_id`, `status`, `result` | `uncertainty`, `references` |
| `ghost-recall` | `query` | `scopes` |
| `ghost-remember` | `scope`, `claim`, `text` | `evidence`, `counterevidence` |
| `ghost-challenge` | `record_id`, `text`, `claim` | `evidence` |

`ghost-message.type` is `direct`, `broadcast`, `request`, or `reply`. Delegation timeouts use a positive Go duration string. The parser checks the schema; the router checks IDs, authority, task state, permissions, and recipients.

Memory blocks run for the authenticated session agent. Recall returns a system
message through the broker after the session reaches an idle boundary.
Remember and challenge only queue writes; an agent's `instruction` claim is
downgraded to an observation. Human memory writes and recalls use
`Controller.RememberFromHuman` and `Controller.RecallFor`.

## Events

The broker emits `core.Event` values for accepted, rejected, delivered, and failed messages; task and consultation changes; and capability briefings. Memory service events use category `memory`, pass through a bounded owner queue, and use the same core JSONL event log. The workspace projection rebuilds memory histories and counters per task, agent, and workspace from those events. A full owner queue records dropped-event telemetry instead of blocking delivery.

## Restart recovery

Each controller run creates a new file under `<workdir>/.ghost/events/`. Startup reads all `*.jsonl` files in filename order, then replays the newest 100 files and newest 20,000 events by default. `run.Options.RecoveryLimits` changes either cap. Old files are opened read-only and are never truncated or rewritten. A malformed final line is skipped and reported. A corrupt line before the final line skips that whole file and adds a visible `workspace.recovery_warning` event. Valid event IDs and sequence numbers in a skipped file are still reserved; its records are not restored. `core.EventLog.Open` is not used to replay old files.

`workspace.initialized` stores the workspace and configured agent IDs on first use. Later runs write `workspace.identity_snapshot` so agent IDs stay stable when the configured roster changes. Replay reserves old event IDs and restores the highest sequence for each stream before the new file is written. The controller emits one `workspace.recovered` event with file, event, message, task, and reconciliation counts. The Bloodwire header and events panel show recovery counts; the panel also shows warnings as text.

Provider sessions are new processes after a restart, so no prior provider turn resumes. Every queued, running, or waiting task becomes failed with reason `interrupted: ghost restarted`; children of an interrupted parent also fail, with `cascade` recorded in `task.reconciled`. This terminal result prevents an old side effect from being mistaken for work that can safely resume.

Pending direct, broadcast, reply, and system messages stay pending. The broker delivers them when a recipient session becomes available. A pending request, delegation, or task-result delivery has an uncertain outcome, so it becomes failed with reason `uncertain after restart`; Ghost creates a system notice for its requester and does not send it again. Open consultations also fail with that reason and notify the requester. A delivery already recorded as delivered is never sent again.

Memory writes that have `memory.write_queued` but no stored, failed, dropped,
skipped, or lost event become `memory.write_lost` with reason
`ghost restarted before write completed`. Recovery does not resend them and
includes their counts in `workspace.recovered`.

See [runtime contracts](runtime.md), [Bloodwire TUI](tui.md), and [headless and runtimes](headless-and-runtimes.md).
