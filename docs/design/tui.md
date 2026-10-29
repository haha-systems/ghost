# Bloodwire TUI

Status: Implements the local controller projection, broker wiring, memory
inspector, and Milestone E human supervision. See
[`IMPLEMENTATION-STATUS.md`](../IMPLEMENTATION-STATUS.md) for test results and
known limits.

## Main view

The default screen is a shared conversation. Its agent tabs, messages, tasks,
activity, and CES status come from `run.Controller.WorkspaceSnapshot` and
`run.Controller.Status`; the TUI does not keep a second copy of workspace
state. Each tab keeps its own selected row and scroll offset. The selected
agent's current activity appears below the tabs when available.

Tab `0` shows all shared messages. Tabs `1` through `9` show messages sent by
or addressed to that agent, broadcasts, tasks involving that agent, and its
activity. The current config type stores agents in a Go map, so the projection
uses alphabetical name order. When there are more than nine agents, `+N`
shows the number not on tabs. Use Ctrl+P or `:agent <name>` to select any
configured agent.

The conversation includes human and agent messages, task cards, QAC decision
summaries, failures, and compact tool counts. The current CES phase is one
status line under the CES policy in the header, not a conversation row for
every phase event. Agent messages
show both ends, for example `WRAITH → @veil`. Delegation, consultation, and
result messages use compact cards with a parent task link and delivery text.
Pending and failed deliveries show their state and failure reason without
relying on color. Raw provider, session, usage, and low-level tool records stay
in the events panel. Agent status uses a glyph and text (`● busy`, `○ offline`,
or `! error`) so it remains clear without color. Rendering follows the
terminal color profile; golden tests use ASCII output.

## Events panel

Ctrl+E or `:events` opens the inspector. At 120 columns or wider it appears
beside the conversation; at smaller widths it uses the full view. It reads
events from the controller projection and supports:

- `a`: toggle the selected-agent filter.
- `k`: cycle event categories; `all` includes every category.
- `/`: search event type, source, actor, and payload text.
- `f`: follow new events or pause at the selected event.
- Enter: show or hide the selected event's raw JSON payload.
- Up/Down and Page Up/Page Down: move through events, including past the last
  event that fits in the pane and through an expanded payload. Home and End
  jump to the oldest event and back to follow. The wheel scrolls the pane
  under the pointer.

Closing the panel only hides it. The event records remain in the controller
projection.

## Memory inspector

Ctrl+P opens the command palette; select `:memory`, or type `:memory`. The
inspector takes no permanent screen space. At 120 columns or wider it appears
beside the conversation. At smaller widths it uses the full view. It uses the
selected task's history when a task is selected, otherwise the selected agent
tab's history, otherwise workspace history.

The inspector shows the latest retrieved records and context injection IDs,
token count, and truncation state. It lists queued, stored, failed, dropped,
skipped, and lost writes with their reasons, plus retrieval latency and memory
counters. Up and Down select a record; Enter opens full provenance, evidence,
counterevidence, source references, backend state, and conflict or superseded
state. Memory is labelled as untrusted context. The inspector has no correction
or deletion controls. It shows `Memory off (not configured)` when disabled and
the recorded reason when setup is unavailable.

`:recall <query>` uses the active agent and opens the returned package in the
inspector. It does not send a message to the agent. `:remember <text>` queues a
workspace instruction for the active agent and displays the queue state.
Memory events and record snapshots are replayed from the workspace event log,
so the inspector can show retrieved records after restart.

## Decision inspector and controls

`:decisions` opens a panel built from decision events in the controller's
workspace snapshot. It groups request, recommendation or failure, and execution
events by request ID. Select a group and press Enter to inspect the full request
state, actions, constraints, memory evidence, and budget, then the full output
and any applied, overridden, or ignored record. Use arrows or Page Up/Down to
scroll the detail. The header shows whether a decision service is present. In
CES auto mode it also shows a pending request or the recommended action, the
executed action, and its source.

`:decision override ID ACTION` sends an operator choice to the controller for
the pending CES auto decision. The command reports controller errors as a
notice. `:ces on`, `:ces off`, and `:ces auto` set the workspace policy. The
`c` key cycles off → on → auto → off. A manual on/off choice takes precedence
over a pending auto choice; use `:ces auto` to resume automatic choices.

`:auto-consult on|off` toggles a bounded consultation after a delegated child
task fails. It is off by default. `:delegation off` rejects new agent
delegations with a recorded reason; existing child tasks continue. Use
`:delegation on` to resume new delegations.

## Focus and keys

The composer is always visible. The default recipient follows the selected
tab (`@all` or `@agent`). A message with explicit `@mentions` uses those
recipients; `@all` sends a broadcast. Enter submits. Alt+Enter or Shift+Enter
inserts a newline. The composer wraps and grows, up to eight lines, so a
longer prompt stays visible; Left, Right, Home, and End move the cursor.
Backspace removes the character before the cursor. Ordinary text is never
treated as a tab or command shortcut while the composer has focus.

Tab and Shift+Tab cycle focus among the composer, conversation, and open events,
memory, or decisions panel. Escape closes the active detail, search, or panel,
or returns focus to the conversation. In the conversation,
Up/Down select rows and Enter opens a selected task card. The task view shows
its owner, participants, status, parent and children, dependencies, CES policy,
and messages for that task and its descendants. Press `x` in task detail to
cancel that task and its open descendants. Escape returns to the same
conversation selection. `:ces view` opens the existing epistemic view.

| Key | Action |
| --- | --- |
| `0`–`9` | Select a tab when the composer is not focused |
| `c` | Cycle CES off, on, and auto |
| Ctrl+E | Toggle events panel |
| Ctrl+P | Open the command palette |
| Ctrl+X | Cancel the active run |
| `x` | Cancel the open task in task detail |
| Ctrl+C | Quit |
| `f` | Toggle event follow when events have focus; otherwise toggle follow state |
| `/` | Search events |
| `?` | Toggle key help |
| Tab / Shift+Tab | Move focus |

The `c`, `f`, `/`, and number shortcuts only act outside composer text input.
In the events pane, `k` cycles event categories and `a` toggles the agent
filter. Ctrl+X retains the existing interrupt/cancel action.

## Commands and palette

Type a colon command in the composer and press Enter:

- `:ces on` / `:ces off` / `:ces auto`: set the workspace CES policy.
- `:ces view`: open the epistemic view.
- `:events`: toggle the events panel.
- `:memory`: open or close the memory inspector.
- `:decisions`: inspect decision requests, outputs, and execution records.
- `:decision override <id> <action>`: apply a human choice to a pending CES decision.
- `:auto-consult on|off`: enable or disable bounded consultation after child failure.
- `:delegation on|off`: allow or stop new agent delegations.
- `:recall <query>`: retrieve records for the active agent and show the package without messaging the agent.
- `:remember <text>`: queue a workspace instruction for the active agent.
- `:agent <name>`: select any configured agent, including overflow agents.
- `:cancel`: cancel the active run.
- `:cancel <task-id-or-prefix>`: cancel one task and its open descendants.
- `:help`: show key help.
- `:legacy`: open the former dashboard; Escape returns to the conversation.
- `:quit`: quit Ghost.

Ctrl+P opens a small filterable list of these commands and configured agents.
Enter applies the selected item.

The header shows controller status. It includes decision service availability,
CES auto progress, and a pending policy change. Unsupported CES is shown with
its reason. The header does not use a local toggle value.

## Projection and limits

The router owns message and task records. The controller's workspace
projection copies those records and memory histories and is the source for the
conversation, events, and memory views. The TUI caches formatted rows and event
filters, updates them from new core events, and rebuilds rows when task state
changes. This avoids formatting the full history for each key press or render.
Memory and conversation event replay after process restart is implemented; see
[`headless-and-runtimes.md`](headless-and-runtimes.md).

The configured agent set is currently represented by a map, so source-file
ordering cannot be retained. Tabs use alphabetical name order until the
configuration model carries explicit order metadata.
