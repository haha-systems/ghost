# Bloodwire TUI

Status: Implements the local controller projection, Milestone C Step 2 broker
wiring, and Milestone D Step 4 memory inspector. See
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
summaries, CES phase changes, failures, and compact tool counts. Agent messages
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
- Up/Down and Page Up/Page Down: move through events.

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

## Focus and keys

The composer is always visible. The default recipient follows the selected
tab (`@all` or `@agent`). A message with explicit `@mentions` uses those
recipients; `@all` sends a broadcast. Enter submits. Backspace removes the
last character. Ordinary text is never treated as a tab or command shortcut
while the composer has focus.

Tab and Shift+Tab cycle focus among the composer, conversation, and open events
or memory panel. Escape closes the active detail, search, events view, or
memory inspector, or returns focus to the conversation. In the conversation,
Up/Down select rows and Enter opens a selected task card. The task view shows
its owner, participants, status, parent and children, dependencies, CES policy,
and messages for that task and its descendants. Press `x` in task detail to
cancel that task and its open descendants. Escape returns to the same
conversation selection. `:ces view` opens the existing epistemic view.

| Key | Action |
| --- | --- |
| `0`–`9` | Select a tab when the composer is not focused |
| `c` | Request the opposite CES policy from the controller |
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

- `:ces on` / `:ces off`: request a CES policy from the controller.
- `:ces view`: open the epistemic view.
- `:events`: toggle the events panel.
- `:memory`: open or close the memory inspector.
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

The header shows the controller's effective CES state. A policy change that
is still waiting for active work is marked `pending`; unsupported CES is
shown with its reason. The header does not use a local toggle value.

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
