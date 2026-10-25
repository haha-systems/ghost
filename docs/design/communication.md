# Communication and events

Status: Milestone C, Step 2 wires the in-memory broker to the controller, runtime sessions, and Bloodwire. The fake runtime covers the Phase 2 acceptance cases listed in `IMPLEMENTATION-STATUS.md`. Step 3 restart recovery is not done.

## One source of truth

`internal/comms.Router` owns conversation messages, delivery state, delegated tasks, consultation state, and broker events. `run.Controller` creates one router for its workspace. `WorkspaceSnapshot` copies the router's task and message records for reads; it does not create a second message record. The router's events pass once through the controller's core event store and the same JSONL `EventLog` as runtime events.

The controller owner accepts requests on its request queue. A serial broker worker runs router operations and session delivery. The router releases its mutex before it calls the delivery adapter. The worker calls the owner only after the router operation returns. Provider `Send` and `Steer` calls run on the worker, outside the owner loop. This keeps provider work from holding router state or blocking controller state changes.

The broker and its projections live in memory. Step 3 will define how replay rebuilds them. Current event logging does not recover tasks, messages, sessions, or delivery attempts after restart.

## Messages and delivery

The sender comes from the authenticated caller or the runtime session that produced the text. The parser does not accept a sender field. The router validates the sender, recipients, task membership, and `CanMessage` permission before it stores one logical message.

A broadcast expands to configured agents other than its sender. It keeps one message ID and records delivery per recipient. Delivery is `pending` while any recipient is pending, `delivered` when all are delivered, and `failed` when no recipient is pending and at least one failed. Failure reasons remain on the message.

An idle recipient session gets `Session.Send`; a running session gets `Session.Steer`. A recipient with no session remains pending. Installing an available session retries pending deliveries. A failed or stopped session fails pending messages with a reason. The TUI shows the status and reason as text.

Every delivered input has a frame with sender, message ID, task ID, thread ID, type, and content. Delegation frames also include the parent task, objective, expected deliverable, effective limits, permissions, and CES policy. A concise capability brief is added to the session instructions at startup or to its first delivered input. The brief is also recorded as a `coordination.capability_briefed` event.

## Delegation and consultation

`Delegate` creates a queued child task. The requester must own the open parent task. The child records requester, target owner, parent ID, objective, context references, expected deliverable, depth, CES policy, permission policy, resource limits, deadline, and correlation ID. The broker sends a `task_delegation` message to the target. `Complete` and `Fail` close the child and send a `task_result` message to its requester.

The CES rule is to inherit the parent's policy unless an override keeps or strengthens it under `off < observe < enforce`. Permissions are intersected across the parent task, requester, target, and requested child policy. Resource limits can only get tighter. An `ask` mode or `requires_approval` request is rejected; approval is not granted by this broker.

Child tasks use the standard runtime session path. The child policy is stored and inherited, but Step 2 does not start CES orchestration for a child. It records this limit and does not claim a CES child run.

`Consult` sends a question, evidence, and references without creating a child task or changing ownership. `ResolveConsultation` returns a separate opinion with findings and uncertainty. Ghost does not merge the opinion into task facts.

Defaults are delegation depth 3, one visit per agent in a delegation chain, 50 agent messages per task, 2 open consultations per consultant, a 10-minute task timeout, and a 10-minute maximum pending-message age. Duplicate open delegation requests and cycles are rejected. `Cancel` marks an open task and its descendants cancelled, cancels linked consultations, sends result notices, and interrupts active task owners through their existing sessions. `Controller.CancelTask(id)` exposes task cancellation; `Controller.Cancel()` cancels the active run and its children.

## Agent channel protocol

`internal/cognition.ParseAgentChannel` parses at most one strict block in a final provider message. It accepts one of `ghost-message`, `ghost-delegate`, `ghost-consult`, or `ghost-result`, all with `schema_version: 1`. It rejects malformed recognized blocks, duplicate or unknown JSON fields, invalid values, and content above 16 KiB. Blocks inside quoted text or other code fences are ignored. Ordinary text and bare mentions such as `@veil` do not route.

The controller supplies the sender from the session that produced the final text. A claimed sender field is rejected. Parsed requests call `Router.Send`, `Delegate`, `Consult`, or `Complete`/`Fail`. A `ghost-result` is accepted only from the owner of an open child task. A rejection becomes a visible `message.rejected` event and one short notification to the agent for that task, which prevents notification loops. Plain final text is stored as a reply to the task's request; raw provider events remain in the events panel.

| Fence | Required fields | Optional fields |
| --- | --- | --- |
| `ghost-message` | `recipients`, `thread_id`, `type`, `content` | `task_id`, `reply_to` |
| `ghost-delegate` | `target`, `parent_task_id`, `objective`, `context_refs`, `expected_deliverable` | `timeout`, `ces_policy`, `permissions` |
| `ghost-consult` | `target`, `parent_task_id`, `question` | `evidence`, `references` |
| `ghost-result` | `task_id`, `status`, `result` | `uncertainty`, `references` |

`ghost-message.type` is `direct`, `broadcast`, `request`, or `reply`. Delegation timeouts use a positive Go duration string. The parser checks the schema; the router checks IDs, authority, task state, permissions, and recipients.

## Events

The broker emits `core.Event` values for accepted, rejected, delivered, and failed messages; task and consultation changes; and capability briefings. Events use the same workspace stream, event log, and UI event panel as runtime events. Event categories remain `conversation`, `task`, or `coordination`. Runtime telemetry is stored as events and does not create duplicate broker messages.

See [runtime contracts](runtime.md), [Bloodwire TUI](tui.md), and [headless and runtimes](headless-and-runtimes.md).
