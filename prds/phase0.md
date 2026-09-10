# Ghost — Phase 0 Project Scaffolding

## Status

Draft

## Purpose

Establish the initial Go project structure, TUI application shell, theme architecture, configuration foundation, logging/event primitives, and developer tooling for Ghost.

This phase must **not** implement the agent runtime.

The goal is to finish with a small, clean, runnable application that establishes the architectural and visual foundations needed for later work.

---

# 1. Product Context

Ghost is a hyper-efficient coding-style agent harness.

Its long-term focus is:

- efficient use of model context;
- subscription-backed agent runtimes such as Codex and Claude Code;
- direct API-backed model runtimes;
- deeply integrated persistent memory;
- efficient tool execution and result compression;
- QAC-based cognitive resource allocation;
- multi-agent supervision;
- CLI/TUI-first operation.

None of those systems are implemented in this phase.

Phase 0 establishes the structure they will eventually inhabit.

---

# 2. Phase Goal

After this phase, a developer must be able to run:

```bash
go run ./cmd/ghost
```

and enter a functioning Ghost TUI.

The TUI should:

- render the Ghost application shell;
- use the default Bloodwire theme;
- adapt to terminal resizing;
- display placeholder agent data;
- allow basic keyboard navigation;
- switch between dashboard and agent-detail views;
- provide a non-functional global steering input;
- provide an event/log area;
- quit cleanly.

The application should also have:

- a clean package structure;
- configuration loading;
- structured logging;
- a basic internal event model;
- theme abstractions;
- unit tests;
- formatting/lint/test commands;
- CI.

No real AI provider should be contacted.

---

# 3. Explicit Non-Goals

Do not implement:

- Codex App Server integration;
- Claude Code integration;
- OpenAI-compatible APIs;
- an agent loop;
- QAC;
- Ghostdive;
- Mem0;
- MCP;
- file-editing tools;
- shell execution tools;
- context budgeting;
- tool-output compression;
- token counting;
- model authentication;
- session persistence;
- git worktrees;
- autonomous task execution;
- provider-specific configuration;
- real quota tracking.

Do not add abstractions for speculative future functionality unless required to establish a clear package boundary.

This phase is scaffolding, not architecture astronautics.

---

# 4. Language

Use Go.

Use the current stable Go toolchain supported by the development environment.

The project should use idiomatic modern Go.

Avoid unnecessary frameworks and dependencies.

---

# 5. TUI Stack

Use the Charm ecosystem.

Required:

```text
Bubble Tea v2
Bubbles v2
Lip Gloss v2
```

Module paths:

```text
charm.land/bubbletea/v2
charm.land/bubbles/v2
charm.land/lipgloss/v2
```

Use Charm components where they solve an actual UI problem.

Do not build custom replacements for existing Bubbles components without reason.

---

# 6. Core Design Principle

Ghost should feel like:

> a restrained operator console for synthetic agents.

The interface should be:

- minimal;
- fast;
- keyboard-first;
- information dense without feeling crowded;
- atmospheric without being decorative;
- readable for long periods.

The visual direction is cyberpunk, but specifically **not** fake-hacker aesthetics.

Avoid:

- Matrix rain;
- glitch effects;
- skull icons;
- fake intrusion messages;
- excessive neon;
- constant animation;
- novelty terminal effects.

Think:

```text
Ghost in the Shell
industrial control system
synthetic-mind operations console
```

rather than:

```text
Hollywood hacking screen
```

---

# 7. Default Theme

The initial built-in theme is:

```text
Bloodwire
```

Bloodwire should use:

- near-black background;
- dark charcoal/red surfaces;
- warm off-white primary text;
- muted red-grey secondary text;
- deep crimson separators;
- brighter red active states;
- hot red only for high-importance states.

Exact colors may be adjusted visually during implementation.

Suggested starting palette:

```text
background     #070506
surface        #0D080A

text           #E8DFE1
text_muted     #73545B

accent_dim     #681323
accent         #C51F3B
accent_hot     #FF3455

border         #351017
```

Do not use bright red everywhere.

The brightest accent must be scarce enough to retain meaning.

---

# 8. Theme Architecture

Themes must be first-class from the beginning.

Do not scatter hard-coded colors throughout views.

Introduce a central theme type.

Conceptually:

```go
type Theme struct {
    Name string

    Colors Colors
    Symbols Symbols
    Borders Borders
}
```

Example:

```go
type Colors struct {
    Background string
    Surface    string

    Text      string
    TextMuted string

    AccentDim string
    Accent    string
    AccentHot string

    Border string

    Error   string
    Warning string
    Success string
}
```

Theme ownership may later grow to include:

- spinner style;
- status glyphs;
- density;
- progress indicators;
- separator style.

Do not implement an elaborate theme engine yet.

Only establish the boundary.

---

# 9. Theme Storage

Ship Bloodwire as a built-in theme.

Design the theme package so external theme files can be supported later.

External theme loading is optional for Phase 0.

Do not delay the phase to implement it.

---

# 10. Initial Application Views

Implement two basic views.

## 10.1 Dashboard

The dashboard is Ghost's default view.

It should approximately contain:

```text
┌─ GHOST ────────────────────────────────────────────────────┐
│                                                          │
│ SYSTEM   3 AGENTS                              QAC  OFF    │
│                                                          │
│ > global steering...                                     │
│                                                          │
├──────────────────────────────────────────────────────────┤
│ AGENT        CLIENT       STATE        ACTIVITY           │
│                                                          │
│ ● VEIL       CODEX        ACTIVE       indexing repo      │
│ ● WRAITH     CLAUDE       ACTIVE       running tests      │
│ ◌ SHADE      DEEPSEEK     IDLE         awaiting task      │
│                                                          │
├──────────────────────────────────────────────────────────┤
│ EVENT STREAM                                             │
│                                                          │
│ 23:41:02 VEIL    read    internal/auth/session.go        │
│ 23:41:05 WRAITH  exec    go test ./...                   │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

The exact layout can be adapted to terminal dimensions.

The agent data in Phase 0 is mock data.

---

# 11. Agent Detail View

Selecting an agent from the dashboard should open a detail screen.

Example:

```text
GHOST / VEIL                                  CODEX

STATUS     ACTIVE
RUNTIME    18m 42s
MODEL      placeholder
MEMORY     —
QAC        OFF

────────────────────────────────────────────────────────────

LIVE LOG

23:48:12  Searching references...
23:48:14  Running tests...
23:48:17  Inspecting result...

────────────────────────────────────────────────────────────

STEER VEIL ›
```

Again, all data is mock data.

The purpose is to establish navigation and component boundaries.

---

# 12. Navigation

Required keyboard behavior:

```text
↑ / k        previous agent
↓ / j        next agent

Enter        open selected agent
Esc          return to dashboard

Tab          change focus
?            show key help

q            quit when no text input is focused
Ctrl+C       always quit
```

Exact bindings may evolve.

Keyboard bindings should live in a centralized keymap.

Do not scatter literal key checks through the application.

---

# 13. Focus Model

The application should track which interactive region currently owns keyboard input.

Initial focus targets:

```text
agent list
global steering input
event stream
agent steering input
```

Use a simple explicit focus model.

Do not build a generalized window manager.

---

# 14. Global Steering Input

The dashboard should include a text input labelled conceptually:

```text
STEER ALL ›
```

or simply:

```text
>
```

The final visual choice can be made during implementation.

In Phase 0:

- input must accept text;
- Enter should generate a local mock event;
- no agent receives the message;
- input should clear after submission.

Example event:

```text
23:42:12 SYSTEM global steering updated:
         reproduce before refactoring
```

This exists only to establish UI behavior.

---

# 15. Per-Agent Steering Input

The detail view should contain a steering input for the selected agent.

In Phase 0:

- accept text;
- submit on Enter;
- generate a mock event;
- do not communicate externally.

---

# 16. Mock Agent Model

Create a small presentation/domain model for the UI.

Example:

```go
type Agent struct {
    ID       string
    Callsign string

    Runtime string
    State   AgentState

    Activity string
}
```

Possible states:

```text
idle
active
waiting
done
error
```

Do not model future provider internals yet.

This type exists only to support current UI requirements.

---

# 17. Callsigns

The mock agents may use callsigns such as:

```text
VEIL
WRAITH
SHADE
```

These are visual placeholders.

Ghost must not require cyberpunk callsigns in future configuration.

Human-readable agent IDs should remain supported.

---

# 18. Status Presentation

Bloodwire should distinguish states primarily through:

- glyph;
- accent intensity;
- text weight;
- subtle emphasis.

Avoid introducing a rainbow status system.

Suggested symbols:

```text
● active
◌ idle
◆ important system/QAC event
✓ complete
× error
```

Symbols must be centralized in the theme.

---

# 19. Application Event Model

Introduce a small generic event type.

This is an internal Ghost event, not a Bubble Tea message.

Example:

```go
type Event struct {
    Time      time.Time
    Source    string
    Kind      EventKind
    Message   string
}
```

Initial kinds might include:

```text
system
agent
steering
status
error
```

Mock events populate the event stream.

Keep the event model deliberately small.

---

# 20. Event Stream

The dashboard should contain a scrollable event stream.

Use an appropriate Bubbles component such as a viewport.

Requirements:

- automatically follow new events by default;
- allow manual scrolling;
- remain usable on terminal resize;
- visually differentiate timestamp/source/message;
- avoid excessive borders.

---

# 21. Logging

Application diagnostic logs and user-facing Ghost events are different things.

Do not mix them.

Introduce structured application logging for:

- startup;
- config loading;
- fatal errors;
- internal diagnostics.

Logs should default to a file rather than corrupting the TUI stdout.

Suggested location:

```text
~/.local/state/ghost/ghost.log
```

Use platform-appropriate paths where practical.

If that adds unnecessary Phase 0 complexity, use:

```text
.ghost/ghost.log
```

temporarily and document the decision.

---

# 22. Configuration

Introduce initial configuration loading.

Suggested default locations:

```text
./ghost.toml
```

and eventually:

```text
~/.config/ghost/config.toml
```

Phase 0 may support only one or both depending on implementation simplicity.

Initial configuration should contain only things actually used.

For example:

```toml
[ui]
theme = "bloodwire"
```

Do not add speculative provider configuration yet.

---

# 23. Configuration Behavior

Requirements:

- Ghost must run without a configuration file;
- defaults must be sensible;
- malformed config should produce a clear error;
- config parsing must be testable independently from the TUI.

---

# 24. Proposed Repository Structure

Use something close to:

```text
ghost/
├── cmd/
│   └── ghost/
│       └── main.go
│
├── internal/
│   ├── app/
│   │   ├── app.go
│   │   └── update.go
│   │
│   ├── config/
│   │   ├── config.go
│   │   └── config_test.go
│   │
│   ├── event/
│   │   └── event.go
│   │
│   ├── model/
│   │   └── agent.go
│   │
│   └── ui/
│       ├── dashboard/
│       │   ├── model.go
│       │   ├── update.go
│       │   └── view.go
│       │
│       ├── agentdetail/
│       │   ├── model.go
│       │   ├── update.go
│       │   └── view.go
│       │
│       ├── keymap/
│       │   └── keymap.go
│       │
│       ├── theme/
│       │   ├── theme.go
│       │   └── bloodwire.go
│       │
│       └── components/
│
├── testdata/
│
├── .github/
│   └── workflows/
│       └── ci.yml
│
├── ghost.toml.example
├── go.mod
├── go.sum
├── LICENSE
├── README.md
└── Makefile
```

This is guidance, not a rigid requirement.

Codex may adjust package boundaries if there is a demonstrably simpler idiomatic structure.

Do not create empty packages purely because they may someday be useful.

---

# 25. Packages Explicitly Not Required Yet

Do not scaffold empty versions of:

```text
provider/
runtime/
agentloop/
memory/
qac/
tools/
context/
artifacts/
codex/
claude/
openai/
```

Those should appear when their first real behavior is implemented.

Avoid architecture-by-empty-directory.

---

# 26. Root Application Model

Use one top-level Bubble Tea model responsible for:

- terminal dimensions;
- active screen;
- global key handling;
- application lifecycle;
- shared theme;
- shared event state where appropriate.

Individual screens should own their local state.

Conceptually:

```text
App
├── Dashboard
└── AgentDetail
```

The top-level application should not contain every component's update logic.

---

# 27. Responsive Layout

Ghost must handle terminal resize events.

Minimum expectations:

- no panic on narrow terminals;
- no negative widths;
- important content remains visible;
- agent table may truncate activity text;
- event pane adjusts height;
- steering input remains reachable.

Choose a reasonable minimum supported terminal size.

Below the minimum, display a simple message such as:

```text
Terminal too small.
Minimum recommended size: 80x24.
```

Do not attempt heroic layout adaptation for extremely small terminals.

---

# 28. Visual Density

Avoid thick boxes around every component.

Prefer:

- whitespace;
- thin horizontal separators;
- restrained headings;
- one primary frame if useful;
- alignment over decoration.

The TUI should visually resemble a coherent application rather than a collection of bordered widgets.

---

# 29. Animation

Keep animation minimal.

Phase 0 may include one subtle activity spinner for active agents.

Do not animate:

- borders;
- titles;
- background;
- event rows;
- idle agents.

The UI must remain calm while agents are busy.

---

# 30. Error Handling

Fatal startup errors should:

- exit cleanly;
- print a human-readable message;
- use a non-zero exit code.

Runtime TUI errors should:

- become an internal event where possible;
- not crash the application unnecessarily.

---

# 31. CLI Behavior

For Phase 0:

```bash
ghost
```

launches the TUI.

Also support:

```bash
ghost --version
ghost --help
```

Do not build a large CLI command hierarchy yet.

Avoid a CLI framework unless it materially simplifies these requirements.

---

# 32. Build Metadata

Expose build metadata where practical:

```text
version
commit
build date
```

At minimum:

```bash
ghost --version
```

should return a useful development version.

---

# 33. README

Create an initial README containing:

- what Ghost is;
- current project status;
- screenshot placeholder;
- build instructions;
- run instructions;
- keyboard controls;
- development commands;
- current MVP scope;
- explicit statement that provider integration is not yet implemented.

Keep future-roadmap material brief.

---

# 34. Development Commands

Provide simple commands for:

```bash
make build
make run
make test
make fmt
make vet
```

A Makefile is acceptable.

Do not introduce a complex task runner.

---

# 35. Testing

Phase 0 should contain tests for logic rather than terminal pixel perfection.

Required areas:

## Config

- defaults;
- valid config;
- invalid config.

## Theme

- Bloodwire theme exists;
- required theme fields are populated.

## Navigation

- dashboard → agent detail;
- agent detail → dashboard;
- agent selection changes correctly.

## Steering

- global mock steering generates an event;
- agent mock steering generates an event.

## Resize

- resize messages update dimensions;
- narrow terminal does not panic.

Avoid brittle full-screen golden tests unless clearly beneficial.

---

# 36. CI

Add a GitHub Actions workflow.

On pull request and main branch push:

```text
go test ./...
go vet ./...
go build ./cmd/ghost
```

Formatting may also be checked.

Keep CI fast.

---

# 37. Performance Expectations

Phase 0 should:

- start essentially instantly;
- remain responsive during resize;
- use negligible CPU while idle;
- avoid unnecessary periodic ticks.

Only active animations should require timer ticks.

Ghost's eventual identity is efficiency.

Its own UI should demonstrate that principle.

---

# 38. Dependency Discipline

Every dependency should have a clear reason to exist.

Required Charm dependencies are expected.

Additional libraries for TOML parsing or similarly small infrastructure are acceptable.

Avoid bringing in large dependency trees for functionality that can be implemented clearly with the standard library.

---

# 39. Architecture Rule

Do not optimize this scaffold for hypothetical future requirements.

Optimize it for making the next phase easy to add.

The next likely architectural work will involve a runtime/session abstraction for:

```text
Codex App Server
Claude Code
Ghost-native API sessions
```

Phase 0 should leave room for that without attempting to design it now.

---

# 40. Definition of Done

Phase 0 is complete when:

1. `go build ./...` succeeds.
2. `go test ./...` succeeds.
3. `go vet ./...` succeeds.
4. `ghost` launches a functioning Bubble Tea v2 TUI.
5. Bloodwire is the default theme.
6. The dashboard displays mock agents.
7. Agent selection works.
8. Agent detail view works.
9. Global steering input accepts mock input.
10. Per-agent steering accepts mock input.
11. Mock events appear in the event stream.
12. Terminal resizing works safely.
13. Keyboard help exists.
14. Configuration defaults work without a file.
15. Structured diagnostic logging exists.
16. CI is configured.
17. No real provider/model integration exists.
18. No agent loop exists.
19. No QAC integration exists.
20. No memory integration exists.

---

# 41. Expected Final Result

Running Ghost should produce the first recognizable version of the product:

```text
┌─ GHOST ────────────────────────────────────────────────────┐
│                                                          │
│ SYSTEM   3 AGENTS                              QAC  OFF    │
│                                                          │
│ STEER ALL ›                                               │
│                                                          │
├──────────────────────────────────────────────────────────┤
│ AGENT        CLIENT       STATE        ACTIVITY           │
│                                                          │
│ ● VEIL       CODEX        ACTIVE       indexing repo      │
│ ● WRAITH     CLAUDE       ACTIVE       running tests      │
│ ◌ SHADE      DEEPSEEK     IDLE         awaiting task      │
│                                                          │
├──────────────────────────────────────────────────────────┤
│ EVENT STREAM                                             │
│                                                          │
│ 23:41:02 VEIL    read    internal/auth/session.go        │
│ 23:41:05 WRAITH  exec    go test ./...                   │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

It does not need to do anything intelligent yet.

It needs to establish that **this is Ghost**.