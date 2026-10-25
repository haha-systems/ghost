# Ghost Next

Status: Phase A audit and contract package are implemented. Runtime wiring is
not started.

Ghost Next moves Ghost from a roster of provider sessions to a runtime that
owns agents, tasks, messages, permissions, memory, routing, and orchestration.
Provider adapters connect that runtime to model services. The terminal UI reads
normalised events and sends control requests; it does not call providers.

## Boundary rule

Status: Designed.

The runtime owns routing, permissions, tasks, memory, and orchestration.
Adapters own provider integration. The TUI subscribes to normalised events
only. Existing `internal/run`, `internal/runtime`, and `internal/app` paths do
not yet enforce this full boundary; see the [Phase 0 audit](audit/phase0-audit.md).

## Milestones

### A. Audit and core contracts

Status: Implemented.

Record the current architecture and define leaf contracts for agents, tasks,
messages, events, IDs, sequencing, and a local JSONL event log. The contracts
are in [`internal/core`](../internal/core/doc.go). They are not connected to
`run`, `serve`, or `app`.

### B. Multi-agent TUI

Status: Designed.

Add agent tabs, a shared conversation, an events inspector, `@agent` and `@all`
addressing, and CES controls. Drive each view from runtime state and
normalised events. See [TUI design](design/tui.md).

### C. Runtime tasks and communication

Status: Not started.

Create runtime-owned task and message records, task assignment and delegation,
address resolution, delivery tracking, and permission checks. Store changes as
events so views and recovery use the same record.

### D. Provider adapters

Status: Not started.

Move provider details behind adapter interfaces. Keep Codex support and add
tested Claude and OpenAI-compatible adapters. Each adapter reports its actual
capabilities and emits the shared event form. Do not claim support from a
configuration name alone.

### E. Memory and CES

Status: Designed.

Keep CES as the evidence and work-state record. Add memory as a separate,
provenance-bearing source that CES can request and cite. Memory must not replace
CES state or silently change its decisions. See [memory design](design/memory.md)
and [CES](CES.md).

### F. Decisions and recovery

Status: Designed.

Record QAC and Jev inputs, decisions, and outcomes as decision events. Recover
runtime projections from the event log, restore stream counters, and mark work
that may have had side effects as interrupted. Do not repeat uncertain writes
or commands during recovery. See [decision design](design/decisions.md) and
[headless and runtimes](design/headless-and-runtimes.md).
