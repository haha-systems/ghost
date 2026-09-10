# Ghost — Phase 1

## Managed Session Runtime + Codex App Server Vertical Slice

## 1. Purpose

Phase 0 established Ghost's project structure and TUI shell.

Phase 1 introduces the first real agent-harness behavior.

The goal is deliberately narrow:

> Ghost must be able to launch, observe, steer, interrupt, and terminate a real Codex-backed agent session.

This phase establishes the runtime/session abstraction that later integrations will use for:

- Claude Code;
- Ghost-native OpenAI-compatible API agents;
- Ghostdive-backed memory;
- QAC;
- global multi-agent steering.

Only Codex is implemented in this phase.

---

# 2. Core Outcome

After Phase 1, a user should be able to configure a Codex agent:

```toml
[agents.backend]
runtime = "codex"
working_dir = "."
```

launch Ghost:

```bash
ghost
```

select the agent, submit:

```text
STEER BACKEND › inspect this repository and tell me what it does
```

and watch the actual Codex session work.

Ghost should display live structured activity such as:

```text
GHOST / BACKEND                                  CODEX

STATUS     ACTIVE
SESSION    019...
RUNTIME    02m 14s

────────────────────────────────────────────────────────────

LIVE ACTIVITY

19:42:03  thinking   inspecting repository structure
19:42:05  command    find . -maxdepth 2 -type f
19:42:07  reading    README.md
19:42:09  reading    go.mod
19:42:13  response   This repository implements...

────────────────────────────────────────────────────────────

STEER BACKEND ›
```

The user must also be able to interrupt the active turn.

---

# 3. Scope

Implement:

- generic runtime abstraction;
- generic agent session abstraction;
- generic session state machine;
- configured agents;
- Codex App Server process lifecycle;
- Codex App Server protocol client;
- thread creation;
- turn creation;
- streamed event handling;
- live TUI integration;
- per-agent steering;
- active-turn interruption;
- clean shutdown;
- runtime error handling;
- basic runtime/session statistics;
- testing with a fake runtime;
- Codex protocol tests using a fake process/server where practical.

Do not implement:

- Claude Code;
- OpenAI-compatible APIs;
- Ghost's own model loop;
- QAC;
- Ghostdive;
- Mem0;
- tool-output compression;
- Ghost-owned tools;
- context budgeting;
- multi-model routing;
- automatic delegation;
- global multi-agent steering;
- persistent Ghost sessions across restarts;
- autonomous agent startup prompts;
- background task queues.

---

# 4. Architectural Principle

Ghost should think in terms of:

```text
Agent
  ↓
Runtime
  ↓
Session
```

Not:

```text
Provider
```

A runtime defines how a particular agent environment is controlled.

For Phase 1:

```text
Ghost
  ↓
Codex Runtime
  ↓
Codex App Server
  ↓
Codex Thread
  ↓
Codex Turn
```

Later:

```text
Ghost
  ├── Codex Runtime
  ├── Claude Runtime
  └── Native Runtime
        ↓
      OpenAI-compatible API
```

---

# 5. Runtime Interface

Introduce a small runtime abstraction.

Conceptually:

```go
type Runtime interface {
    Name() string
    Capabilities() Capabilities

    Start(
        ctx context.Context,
        cfg SessionConfig,
    ) (Session, error)

    Close() error
}
```

The exact Go API may differ if a cleaner design emerges.

Keep the interface small.

---

# 6. Session Interface

A Session represents one long-lived Ghost agent.

Conceptually:

```go
type Session interface {
    ID() string

    State() SessionState

    Send(
        ctx context.Context,
        input Input,
    ) error

    Steer(
        ctx context.Context,
        input Input,
    ) error

    Interrupt(
        ctx context.Context,
    ) error

    Events() <-chan Event

    Stats() SessionStats

    Close() error
}
```

Important:

`Send` and `Steer` are distinct operations.

Do not collapse them merely because one backend can sometimes treat them similarly.

---

# 7. Capability Model

Different runtimes will support different behavior.

Introduce capability discovery immediately.

Example:

```go
type Capabilities struct {
    Steering       bool
    Interrupt      bool
    Resume         bool
    Usage          bool
    ToolEvents     bool
    ReasoningEvents bool
}
```

Do not design for every hypothetical capability.

Only include capabilities required or strongly implied by Codex and the planned Claude/native runtimes.

---

# 8. Agent Configuration

Replace Phase 0 mock-only agents with configured agent definitions.

Example:

```toml
[agents.backend]
runtime = "codex"
working_dir = "."
```

Optional:

```toml
[agents.backend]
runtime = "codex"
working_dir = "."
model = "gpt-5.6-sol"
```

Model selection should remain optional.

If no model is configured, allow Codex to use its own configured/default model.

Do not hard-code specific model names into Ghost.

---

# 9. Agent Identity

Separate:

```text
agent ID
```

from:

```text
runtime session ID
```

Example:

```text
Ghost Agent ID:
backend

Codex Thread ID:
019...
```

The dashboard uses the human-friendly Ghost ID.

The detail screen may expose the backend session/thread ID.

---

# 10. Codex Runtime

Implement:

```text
runtime/codex
```

using Codex App Server.

Ghost should invoke the installed official `codex` binary.

Do not implement OpenAI authentication.

Do not copy or manipulate Codex authentication files.

Codex remains responsible for its own authentication and ChatGPT subscription access.

---

# 11. Codex App Server Lifecycle

Ghost should start:

```bash
codex app-server --stdio
```

or the currently supported equivalent.

The Codex runtime owns this subprocess.

On startup:

```text
spawn app-server
      ↓
initialize protocol
      ↓
wait until ready
      ↓
accept session creation
```

On Ghost shutdown:

```text
stop active sessions
      ↓
close protocol client
      ↓
terminate app-server
```

Avoid orphaned processes.

---

# 12. Shared App Server

Prefer one Codex App Server process per Ghost process.

Do not launch one App Server process per agent unless implementation evidence shows this is required.

Each Ghost Codex agent should correspond to a separate Codex thread.

Conceptually:

```text
Ghost

CodexRuntime
    │
    └── App Server
          ├── thread: backend
          ├── thread: tests
          └── thread: review
```

Phase 1 only needs to prove this architecture with one or a small number of sessions.

---

# 13. Protocol Boundary

Do not leak raw Codex protocol objects into the rest of Ghost.

Create:

```text
Codex protocol event
       ↓
Codex adapter
       ↓
Ghost Event
```

Only the Codex package should know about:

```text
thread/start
thread/resume
turn/start
turn/steer
turn/interrupt
item/*
turn/*
```

The TUI must consume generic Ghost session events.

---

# 14. Codex Initialization

Implement the required App Server initialization handshake.

Identify Ghost clearly as the client.

For example:

```text
name: ghost
title: Ghost
version: <build version>
```

Do not enable experimental protocol capabilities unless required by Phase 1.

Prefer stable v2 protocol functionality.

---

# 15. Thread Creation

When a configured Ghost Codex agent is started:

1. validate its working directory;
2. create a Codex thread;
3. retain the returned thread ID;
4. associate the thread with the Ghost agent;
5. mark the Ghost session idle;
6. emit a session-created event.

Do not immediately send a model prompt unless the user has provided one.

---

# 16. Important Persistence Rule

Do not assume that creating a zero-turn Codex thread guarantees durable resumability.

Ghost Phase 1 does not promise session persistence across application restarts.

The Codex ecosystem has recently had behavior where a new thread does not gain its durable rollout state until its first turn.

Therefore:

> Restart persistence is explicitly deferred.

Within one running Ghost process, the session must behave reliably.

---

# 17. Session State Machine

Use an explicit state machine.

Suggested states:

```go
type SessionState string

const (
    StateStarting     SessionState = "starting"
    StateIdle         SessionState = "idle"
    StateRunning      SessionState = "running"
    StateInterrupting SessionState = "interrupting"
    StateFailed       SessionState = "failed"
    StateStopped      SessionState = "stopped"
)
```

Avoid deriving important session state purely from arbitrary event strings.

---

# 18. State Transitions

Expected transitions:

```text
STARTING
   ↓
IDLE
   ↓
RUNNING
   ├──→ IDLE
   ├──→ INTERRUPTING → IDLE
   └──→ FAILED

IDLE
   └──→ STOPPED
```

Invalid transitions should either:

- return an explicit error; or
- be safely ignored when idempotence is appropriate.

---

# 19. Turn Creation

When the session is idle and the user submits input:

```text
Ghost input
    ↓
turn/start
    ↓
record turn ID
    ↓
StateRunning
```

Track the active turn ID explicitly.

Do not infer it from UI state.

---

# 20. Turn Admission Safety

Ghost must serialize user operations for a session.

Do not allow:

```text
turn/start
turn/start
turn/start
```

to race concurrently.

This matters because Codex currently treats `turn/start` as capable of joining/steering an already-active regular turn under some conditions.

Ghost must decide explicitly whether the user's action means:

```text
start new work
```

or:

```text
steer existing work
```

before sending the protocol request.

---

# 21. Per-Agent Steering

When an agent is actively running, text entered into:

```text
STEER BACKEND ›
```

should use Codex's steering mechanism.

Conceptually:

```text
user steering
    ↓
session.Steer()
    ↓
turn/steer
    ↓
existing active turn
```

Steering must not create an accidental independent turn.

---

# 22. Input While Idle

If the agent is idle, the same detail-screen input acts as a new instruction.

Conceptually:

```text
IDLE + input
   ↓
Send()
   ↓
new turn
```

Therefore the UI may expose one input box while the session abstraction distinguishes the underlying operation.

---

# 23. Active Turn Interruption

Add an explicit interrupt key binding.

Suggested:

```text
Ctrl+X
```

or another non-conflicting binding.

The help overlay must show it.

When triggered:

```text
active session
      ↓
current turn ID
      ↓
turn/interrupt
      ↓
StateInterrupting
      ↓
terminal turn event
      ↓
StateIdle
```

---

# 24. Interrupt Race Safety

Current Codex interruption targets a specific turn.

Ghost must therefore track:

```text
thread ID
turn ID
```

together.

Do not:

- discover a newer turn and retry interruption automatically;
- guess which turn to stop;
- blindly retry an interrupt against a changed turn.

If an interrupt fails because the turn changed, surface the failure safely.

Do not risk stopping work that began after the user's original interrupt action.

---

# 25. Event Stream

Codex App Server notifications should feed Ghost's existing event infrastructure.

Translate useful events into categories such as:

```text
session
thinking
message
command
file
tool
usage
status
error
```

Do not expose raw JSON in the primary UI.

Raw protocol messages may be available in diagnostic logs.

---

# 26. Event Normalization

Create a generic Ghost event structure suitable for all future runtimes.

Example:

```go
type Event struct {
    Time      time.Time
    AgentID   string
    Kind      EventKind

    Summary   string
    Detail    string

    Metadata map[string]string
}
```

Keep it intentionally lossy for presentation.

Do not attempt to create one enormous union covering every Codex event type.

---

# 27. Raw Event Preservation

For debugging, optionally preserve raw backend events in diagnostic logs.

Rule:

> Normalize for the UI, preserve enough raw evidence for debugging.

Do not allow verbose raw events to flood the TUI.

This foreshadows Ghost's later context-efficiency philosophy.

---

# 28. Agent Message Streaming

Stream assistant/model text incrementally when Codex provides deltas.

The detail view should visibly update during the turn.

Do not wait until `turn/completed` to display all output.

The rendering path should batch updates if necessary to avoid excessive TUI redraw overhead.

---

# 29. Tool Activity

Where Codex exposes structured command/tool/file events, convert them into concise activity entries.

Example:

```text
19:42:05  exec      go test ./...
19:42:08  read      internal/auth/session.go
19:42:12  edit      internal/auth/session.go
```

The exact event coverage depends on available stable protocol notifications.

Do not parse natural-language assistant output to infer tool activity when structured events exist.

---

# 30. Dashboard Integration

Phase 0 mock agents should be replaced by real runtime-backed state when configured.

Example:

```text
AGENT      RUNTIME    STATE      ACTIVITY

● backend  CODEX      ACTIVE     running tests
◌ review   CODEX      IDLE       waiting
```

If no agents are configured, Ghost may continue to show an empty-state screen.

Do not silently create fake agents in normal runtime mode.

---

# 31. Agent Detail Integration

The detail pane should expose:

```text
agent ID
runtime
session/thread ID
session state
active turn status
runtime duration
latest activity
live log/event stream
assistant output
steering input
```

Only expose usage statistics that Codex actually provides.

Unknown fields should render as:

```text
—
```

not invented values.

---

# 32. Global Steering

Keep Phase 0's global steering UI visible.

However:

> Global multi-agent steering remains non-functional in Phase 1.

Submitting global steering may continue to emit a local informational event.

Do not prematurely define broadcast semantics.

That behavior will receive its own phase once multiple real runtimes exist.

---

# 33. Startup Behavior

At Ghost startup:

1. load config;
2. build configured runtimes;
3. start Codex App Server if needed;
4. create configured agent sessions;
5. enter TUI;
6. display runtime/session readiness.

Ghost should not block indefinitely if Codex cannot start.

Use a reasonable startup timeout.

---

# 34. Missing Codex

If the user configures:

```text
runtime = codex
```

but `codex` is unavailable:

- Ghost should remain stable;
- the affected agent should enter `failed`;
- the UI should show a clear reason;
- diagnostic logs should contain technical detail.

Example:

```text
× backend   CODEX   ERROR   codex executable not found
```

Do not crash the entire TUI merely because one runtime cannot start.

---

# 35. Authentication Failure

If Codex App Server starts but authentication is unavailable:

surface a useful message.

Example:

```text
Codex authentication required.
Run `codex` and sign in with ChatGPT first.
```

Ghost itself must not implement login in Phase 1.

---

# 36. Process Failure

If App Server unexpectedly exits:

1. mark affected sessions failed;
2. emit a high-priority runtime event;
3. keep Ghost itself running where practical;
4. do not automatically enter an uncontrolled restart loop.

Automatic runtime restart is deferred.

---

# 37. Shutdown

On:

```text
q
Ctrl+C
```

Ghost should:

1. stop accepting new session commands;
2. interrupt or terminate active work safely;
3. close sessions;
4. close App Server;
5. wait briefly for graceful shutdown;
6. kill the subprocess only if necessary;
7. restore terminal state;
8. exit.

No orphaned Codex App Server should remain.

---

# 38. Concurrency Model

Each session should have one authoritative command path.

A good implementation may use:

```text
session command channel
```

with commands such as:

```text
Send
Steer
Interrupt
Stop
```

This makes session sequencing explicit.

Avoid multiple goroutines independently issuing Codex requests for the same session.

---

# 39. Runtime Events and Bubble Tea

Do not make the Codex runtime depend on Bubble Tea.

The dependency direction should be:

```text
Codex Runtime
     ↓
Ghost Events
     ↓
Application
     ↓
Bubble Tea messages
     ↓
TUI
```

Runtime packages must be independently testable.

---

# 40. Proposed Package Structure

Something approximately like:

```text
internal/
├── agent/
│   ├── agent.go
│   └── state.go
│
├── runtime/
│   ├── runtime.go
│   ├── session.go
│   ├── capabilities.go
│   │
│   ├── fake/
│   │   └── fake.go
│   │
│   └── codex/
│       ├── runtime.go
│       ├── session.go
│       ├── client.go
│       ├── protocol.go
│       ├── process.go
│       └── events.go
│
├── event/
│   └── event.go
│
└── ui/
    └── ...
```

Do not follow this mechanically if the existing Phase 0 structure suggests a cleaner integration.

---

# 41. Fake Runtime

Implement a fake Runtime/Session for tests.

It should support:

```text
start
send
steer
interrupt
event emission
state transitions
failure injection
```

This becomes important later when Claude/native/QAC are added.

Do not require real model quota for normal unit tests.

---

# 42. Codex Protocol Testing

Where practical, test protocol behavior against a fake App Server process or transport.

Tests should cover:

- initialize;
- thread creation;
- turn start;
- incoming notifications;
- steering;
- interruption;
- malformed messages;
- process exit;
- request/response correlation.

Do not make the standard test suite contact OpenAI.

---

# 43. Request Correlation

The App Server client must safely associate JSON-RPC responses with requests.

Use unique request IDs.

Support asynchronous notifications arriving between requests and responses.

Do not assume:

```text
send request
read next line
next line is response
```

Notifications may interleave.

---

# 44. Protocol Reader

Prefer a single protocol-reader goroutine.

Conceptually:

```text
stdout
   ↓
reader
   ├── response → pending request
   └── notification → event/session dispatcher
```

Do not have multiple goroutines independently reading App Server stdout.

---

# 45. Backpressure

Event production must not deadlock the runtime merely because the TUI is temporarily slow.

Use bounded buffering.

For high-frequency low-value events, coalescing may be acceptable.

Never silently discard:

```text
errors
turn completion
session state changes
interrupt results
```

---

# 46. Session Statistics

Introduce minimal runtime-independent statistics.

Example:

```go
type SessionStats struct {
    StartedAt     time.Time
    TurnsStarted  uint64
    TurnsFinished uint64
    LastActivity  time.Time
}
```

If Codex supplies token usage, expose it optionally.

Do not build the full Ghost efficiency telemetry system yet.

---

# 47. Usage Data

If stable Codex events expose token usage, record:

```text
input tokens
output tokens
cached tokens
```

where available.

Do not fail a session if usage information is absent.

This data will become valuable later for QAC and Ghost efficiency benchmarking.

---

# 48. Logging

Add runtime-specific diagnostic logging:

```text
codex process started
initialize completed
thread created
turn started
turn completed
steering sent
interrupt requested
app-server exited
```

Do not log:

- authentication tokens;
- secrets;
- sensitive environment variables.

Raw prompts may be omitted from diagnostic logs by default.

---

# 49. Security Boundary

Ghost should not weaken Codex's own sandbox or approval model by default.

Let Codex retain its configured safety behavior.

Phase 1 should not automatically start Codex with:

```text
danger-full-access
approval = never
```

unless the user explicitly configures such behavior later.

---

# 50. Configuration Validation

Validate:

- runtime is known;
- working directory exists;
- working directory is a directory;
- agent IDs are unique.

Unknown configuration keys may follow the project's existing strictness policy.

Errors should clearly name the affected agent.

---

# 51. Example Configuration

Minimal:

```toml
[ui]
theme = "bloodwire"

[agents.backend]
runtime = "codex"
working_dir = "."
```

Multiple sessions should be structurally possible:

```toml
[agents.backend]
runtime = "codex"
working_dir = "."

[agents.review]
runtime = "codex"
working_dir = "."
```

Whether multiple real agents are exercised in the Phase 1 acceptance test is optional.

---

# 52. Empty State

If no agents are configured:

```text
GHOST

No agents configured.

Add one to ghost.toml:

[agents.backend]
runtime = "codex"
working_dir = "."
```

Keep this visually consistent with Bloodwire.

---

# 53. Error State

Failed agents remain visible.

Example:

```text
× REVIEW    CODEX    ERROR

Codex App Server exited unexpectedly.
```

Users should be able to inspect the detail view for more information.

---

# 54. No Automatic Agent Work Yet

Configured agents should start:

```text
IDLE
```

They should not autonomously inspect the repository.

The user initiates work through the agent input.

Automatic goals and autonomous startup behavior are future work.

---

# 55. Why Codex First

Codex is the first runtime because it provides:

- subscription-backed usage;
- structured session/thread lifecycle;
- structured event streaming;
- steering;
- interruption;
- repository agent capabilities already implemented by Codex itself.

This lets Ghost validate its control-plane architecture without rebuilding:

- shell tooling;
- file editing;
- patch application;
- model context handling;
- sandboxing.

---

# 56. Architectural Guardrails

Do not:

- implement Ghost's own agent loop;
- parse Codex terminal UI output;
- shell out to `codex exec` for every user message;
- start a new Codex process per turn;
- expose Codex protocol structures throughout Ghost;
- allow concurrent command races within a session;
- retry interrupts against arbitrary newer turns;
- make the TUI own runtime lifecycle;
- hard-code OpenAI model names;
- build Claude abstractions prematurely;
- build QAC hooks prematurely;
- implement persistent restart/resume yet.

---

# 57. Acceptance Scenario

Given:

```toml
[agents.veil]
runtime = "codex"
working_dir = "."
```

the following must work.

## Startup

```text
ghost
```

Ghost launches.

Dashboard:

```text
AGENT   RUNTIME   STATE

◌ VEIL  CODEX     IDLE
```

## New Turn

User opens VEIL and enters:

```text
Inspect README.md and summarize this project.
```

Ghost:

1. starts a Codex turn;
2. records the turn ID;
3. enters ACTIVE state;
4. streams Codex activity;
5. displays the final response;
6. returns to IDLE.

## Steering

User starts:

```text
Inspect the repository for architectural problems.
```

While active, user enters:

```text
Focus specifically on concurrency.
```

Ghost sends that input to the active turn as steering.

## Interrupt

User starts another long operation.

User invokes the interrupt binding.

Ghost:

1. interrupts the exact active turn;
2. transitions through INTERRUPTING;
3. receives terminal state;
4. returns to IDLE.

## Shutdown

User quits Ghost.

Codex App Server terminates cleanly.

---

# 58. Tests Required

Unit/integration tests must verify:

### State machine

- starting → idle;
- idle → running;
- running → idle;
- running → interrupting;
- interrupting → idle;
- runtime failure → failed;
- invalid transitions.

### Runtime

- session creation;
- send while idle;
- steer while running;
- interrupt active turn;
- reject inappropriate duplicate operations;
- clean shutdown.

### Protocol

- interleaved responses and notifications;
- request correlation;
- malformed protocol input;
- process death.

### UI integration

- runtime state updates dashboard;
- events appear in detail view;
- response streaming updates view;
- errors remain inspectable.

---

# 59. Definition of Done

Phase 1 is complete when:

1. Ghost has a runtime abstraction.
2. Ghost has a session abstraction.
3. Ghost has an explicit session state machine.
4. Agent configuration creates real sessions.
5. Codex App Server starts from Ghost.
6. Ghost initializes the App Server protocol.
7. A Codex thread is created for a Ghost agent.
8. User input starts a real Codex turn.
9. Codex events stream into Ghost.
10. Assistant responses stream into the detail pane.
11. Structured activity appears in the event log.
12. Active turns can be steered.
13. Active turns can be interrupted safely.
14. Session state appears correctly in the dashboard.
15. App Server failure is handled without crashing Ghost.
16. Ghost shutdown does not leave an App Server orphan.
17. Normal tests do not consume model quota.
18. No Claude integration exists yet.
19. No native API loop exists yet.
20. No QAC or external memory integration exists yet.

---

# 60. What Comes Next

Phase 1 establishes:

```text
Ghost
  ↓
Runtime
  ↓
Session
  ↓
Codex
```

The next likely phase should add **Claude Code as the second managed runtime**, forcing the common session abstraction to prove that it generalizes.

Only after two managed runtimes work should Ghost introduce its own OpenAI-compatible native agent loop.

That ordering is deliberate:

```text
Phase 1   Codex
Phase 2   Claude Code
Phase 3   Ghost Native
Phase 4   efficient Ghost tools/context pipeline
Phase 5   native external memory
Phase 6   QAC
```

Do not implement those phases early.

The purpose of Phase 1 is to prove one thing well:

> Ghost can act as a clean, reliable control plane over a real subscription-backed coding agent.