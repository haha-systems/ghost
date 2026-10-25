# TUI event model

Status: Designed. Implementation is not started.

The TUI will subscribe to the runtime's normalised event stream. It will not
read provider sessions or translate provider event kinds. The runtime remains
the source of agent, task, message, permission, and CES state.

Agent tabs show current availability and activity. A shared conversation
projects `core.Message` records by thread. The events inspector shows event
category, actor, task/session scope, sequence, and payload detail. `@agent_id`
and `@all` resolve through the runtime; the UI does not route messages itself.
CES controls send task-scoped requests and render the resulting state events.

Views may keep bounded display caches, but they cannot become state stores.
On reconnect, they resume from the last event sequence and deduplicate by
event ID or stream and sequence. The event replay path must prevent gaps between
replay and live delivery before the TUI relies on it.

Today `app.Model` holds runtime sessions and `internal/app/update.go` maps
runtime event kinds. `internal/ui/*` consumes presentation models. The
[Phase 0 audit](../audit/phase0-audit.md#10-tui-coupling) records the current
boundary; [headless and runtimes](headless-and-runtimes.md) describes related
event replay requirements.
