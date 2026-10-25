# Communication and events

Status: the standalone broker and validated agent-channel parser are implemented and tested. They are not wired to the controller or providers. Broker state is held in memory; restart recovery is Step 3.

## Broker boundary

`internal/comms.Router` is the routing authority. It imports `internal/core` and the Go standard library only. It receives an `AgentDirectory` for current agent records and message authorization, and a `Delivery` transport with `Deliver(ctx, agentID, message) error`. The clock and ID generator are injectable.

The public entry points are `NewRouter(RouterConfig)`, `RegisterTask`, `Send`, `Delegate`, `Consult`, `ResolveConsultation`, `FailConsultation`, `Complete`, `Fail`, `Cancel`, `Tick`, `AgentAvailable`, and `Snapshot`. The router owns message IDs, task IDs, delivery state, consultation state, and coordination events.

`Snapshot` returns detached values for projections.

The router stores accepted messages and state in memory. It does not write a durable broker log. The event records make accepted, rejected, delivered, failed, task, and consultation changes inspectable during the process lifetime. Step 3 will define restart recovery.

## Messages and delivery

The sender is supplied by the caller's authenticated agent context. The message body cannot set a sender or route by itself. The router validates sender and recipients against the directory, checks `CanMessage`, assigns the message ID, stores one logical message, then attempts delivery.

A broadcast expands to configured agents other than the sender. It keeps one message ID and has a delivery state for each recipient. Direct messages can also have more than one explicit recipient. Delivery states are `pending`, `delivered`, and `failed` per recipient. The message-level state is `pending` while any recipient is pending, `delivered` when all recipients were delivered, and `failed` when no recipient is pending and at least one failed. `DeliveryReason` records each failure.

An unavailable recipient stays pending. `AgentAvailable(id)` retries its pending deliveries. A delivery transport error marks that recipient failed and records the error. A pending message older than the configured maximum age becomes failed. The router records `message.accepted`, `message.delivered`, `message.failed`, or `message.rejected` events. A recipient pair is attempted at most once; unavailable recipients are retried only after availability changes.

Rejected messages are not stored as accepted messages. The router records the rejection reason in `message.rejected`. Plain model text, including a bare `@agent` mention, has no routing effect. Only a separately parsed channel request may reach a later controller adapter.

## Delegation

`Delegate` creates one queued child task. The requester must own the open parent task. The child records requester, target owner, parent ID, objective, context references, expected deliverable, depth, CES policy, permission policy, resource limits, deadline, and correlation ID. The broker sends a `task_delegation` message to the target. `Complete` and `Fail` close the child and send a `task_result` message to its requester. Timeouts use the existing `failed` task status and set `failure_reason` to `timeout`.

The CES rule is: inherit the parent's policy when no override is given. An override may keep or strengthen the policy under the order `off < observe < enforce`; a weaker or invalid override is rejected. This rule treats stronger CES enforcement as a stricter child constraint.

The permission rule is an intersection of the parent task policy, requester policy, target policy, and any requested child policy. `allow_list` values are intersected; `deny_list` values are combined; allow lists exclude denied tools. `inherit` adds no permission. An `ask` mode or `requires_approval` flag is rejected with `message.rejected`; the broker does not grant permission while human approval is pending. Child resource limits are the tighter of inherited and requested values. A child cannot raise a parent's budget or deadline.

The defaults are maximum delegation depth 3, maximum visits to one agent in a delegation chain 1, 50 agent messages per task, 2 open consultations per consultant, a 10-minute task timeout, and a 10-minute maximum pending-message age. A request may use stricter limits. Delegation rejects an open duplicate with the same requester, target, parent task, and objective after trimming, lower-casing, and collapsing whitespace. A child at depth `d` has depth `d+1`. Reaching the configured agent-visit bound rejects a cycle such as A to B to A.

`Cancel` marks the task and all open descendants cancelled. It also cancels consultations linked to those tasks. Each state change has an event and a reason. A task or consultation does not hold up the parent's runtime while waiting for its result.

## Consultation

`Consult` sends a question, evidence, and references to a consultant. It creates no child task and does not change task ownership. The per-agent concurrency limit counts open requests for each consultant. `ResolveConsultation` returns a `ConsultationResult` with `Question`, `Evidence`, `Findings`, `Uncertainty`, and `References` in a reply message to the requester. The result stays a separate opinion; Ghost does not merge it into task facts. A failed, cancelled, or timed-out consultation has a status, reason, and coordination event.

## Causal events

The broker emits normal `core.Event` values in the conversation, task, or coordination category. The first event in a thread establishes its root correlation event ID. Tasks and later messages in that thread keep that `CorrelationID`. Delivery events use the `message.accepted` event as `CausationID`; task results use the task transition event; child cancellation events use the parent cancellation event. The envelope uses event IDs for both causal fields, while `ThreadID` and `TaskID` identify the visible conversation and task.

## Agent channel protocol

`internal/cognition.ParseAgentChannel` parses one strict structured block. It returns a typed request and does not send or route it. It accepts at most one channel block in a response. A recognized malformed block returns a precise error. Unknown or duplicate JSON fields, invalid schema versions, missing required fields, null values, and content above 16 KiB are rejected. Blocks inside quoted text or other code fences are ignored. Prose and mentions such as `@veil` are ignored.

Every block uses `schema_version: 1`. The sender is not a JSON field; the caller supplies it from authenticated context.

| Fence | Required fields | Optional fields |
| --- | --- | --- |
| `ghost-message` | `recipients`, `thread_id`, `type`, `content` | `task_id`, `reply_to` |
| `ghost-delegate` | `target`, `parent_task_id`, `objective`, `context_refs`, `expected_deliverable` | `timeout`, `ces_policy`, `permissions` |
| `ghost-consult` | `target`, `parent_task_id`, `question` | `evidence`, `references` |
| `ghost-result` | `task_id`, `status`, `result` | `uncertainty`, `references` |

`ghost-message.type` is `direct`, `broadcast`, `request`, or `reply`. A delegation timeout is a positive Go duration string. The parser validates the schema and field values; the broker or later controller adapter must still validate IDs, authority, current task state, permissions, and recipients.

## Event log and recovery

The controller's current JSONL event log and runtime projections are separate from this broker store. The broker is not wired to that log in this step. No restart recovery or retry after restart is claimed. See [runtime contracts](runtime.md) and [headless and runtimes](headless-and-runtimes.md).
