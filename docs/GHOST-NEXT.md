# Ghost Next

Status: Phase A and milestone B steps 1 and 2 are implemented, including TOML
agent order. Milestone C has a tested broker and protocol parser, not yet wired
to the controller or providers. The Bloodwire TUI is driven by the runtime
projection. See the
[implementation status](IMPLEMENTATION-STATUS.md) for acceptance results and
known limits.

Ghost Next moves Ghost from a roster of provider sessions to a runtime that
owns agents, tasks, messages, permissions, memory, routing, and orchestration.
Provider adapters connect that runtime to model services. The terminal UI
reads normalised events and sends control requests.

## Boundary rule

Status: Partly implemented.

The runtime owns in-memory agent, task, message, and event projections used by
the TUI. `internal/run` still calls the configured runtime directly. Permission
checks, memory, and a full provider adapter boundary are not implemented. See the
[Phase 0 audit](audit/phase0-audit.md) for the earlier design.

## Milestones

### A. Audit and core contracts

Status: Implemented.

The audit and leaf contracts for agents, tasks, messages, events, IDs,
sequencing, and a local JSONL event log are in [`internal/core`](../internal/core/doc.go).

### B. Runtime layer and multi-agent TUI

Status: Steps 1 and 2 implemented. Agent tabs and workspace projection follow
TOML source order, with alphabetical order for map-built configurations.

Step 1 maps runtime and CES events to core events, stores agents, tasks, and
messages in one workspace projection, adds controller routing and CES policy
control, and appends events to a local JSONL log. Step 2 adds the projection-
driven conversation, agent tabs, event inspector, task details, and CES controls.
See [TUI design](design/tui.md).

### C. Delegation, delivery, and permissions

Status: Broker implemented and tested; controller/provider wiring and restart
recovery remain open.

The runtime creates task and message records, resolves configured agent names
and mentions, and stores state changes as events. A standalone broker now
implements message routing, delivery state, child-task delegation,
consultation, permission intersection, limits, and validated agent-channel
parsing. It is not yet connected to the controller or providers. Durable
recovery remains open. A child task inherits its parent's CES policy unless a
same or stronger policy is explicitly requested.

### D. Provider adapters

Status: Not started.

Move provider details behind adapter interfaces. Keep Codex support and add
tested Claude and OpenAI-compatible adapters. Each adapter reports its actual
capabilities and emits the shared event form.

### E. Memory and CES

Status: Partial.

Keep CES as the evidence and work-state record. QAC selects resources for CES;
it does not own CES phases or epistemic state. Add memory as a separate,
provenance-bearing source that CES can request and cite. Memory must not
replace CES state or silently change its decisions. See
[memory design](design/memory.md) and [CES](CES.md).

### F. Decisions and recovery

Status: Partial.

QAC plans and decisions now enter the core event stream. Recover runtime
projections from the event log, restore stream counters, and mark work that may
have had side effects as interrupted. Do not repeat uncertain writes or
commands during recovery. See [decision design](design/decisions.md) and
[headless and runtimes](design/headless-and-runtimes.md).
