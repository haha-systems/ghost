# Headless Ghost and runtime adapters

Design proposal, 12 October 2026. “Current” describes the source listed below.
All new commands, methods, settings, and guarantees are proposed.
This note does not change the implementation or configuration.

## 1. Goal

Let AI agents, including Mark's Grok Bot teammates, drive Ghost for all
development work without the terminal user interface (TUI). Clients must be
able to start work, give instructions, cancel, inspect results, and recover
missed events without terminal input or screen parsing.

Keep the CES authority rules: Ghost selects the phase; QAC selects the
resource; the runtime performs the work. Preserve fresh phase sessions,
projected evidence, artifact validation, repair limits, and independent CLOSE
verification. These rules are described in [docs/CES.md](../CES.md) and
implemented by `Orchestrator.Advance`, `PhaseRunner.Run`, and
`cesController.process` in [cognition](../../internal/cognition/orchestrator.go),
[runner](../../internal/cognition/runner.go), and
[controller](../../internal/run/ces_controller.go).

[GHOST.md](../../GHOST.md), “Autonomous execution”, “QAC authority”, and
“Handoff discipline”, supplies the operating contract. A headless client acts
as the operator; a model must still report evidence rather than select its
own destination resource. Completion must come from CES, not process exit.

## 2. Headless mode

### Current boundary and controller extraction

`cmd/ghost.run` parses `--config` and `--version`, loads configuration, and
starts `tea.NewProgram(app.New(...))`. It has no headless command dispatch.
See [main.go](../../cmd/ghost/main.go), `run`.

`internal/run.Controller` is the UI-free control loop. `cesController.process`
applies CES decisions on that loop and records through the owner. `Model`
projects those events and owns screen state. See
[controller.go](../../internal/run/controller.go), `Start`, `Steer`, `Cancel`;
[ces.go](../../internal/run/ces.go), `startCESWork`, `runPhase`,
`handlePhaseResult`, `finishCESWork`; and
[ces_controller.go](../../internal/run/ces_controller.go), `process`.

Propose a UI-free run controller with Start, Steer, Cancel, Status, Override,
Subscribe, and Close operations. Move lifecycle state and side effects into
it; leave rendering, keyboard handling, and screen state in Bubble Tea's
`Model`. Both command modes and the TUI must call the same controller.
Do not simulate key messages or start a hidden terminal program.

Each run gets one serialized control loop. Runtime readers send immutable
events and results to it. Only that loop changes its store and QAC coordinator.
Keep observer events before the phase result, as `drainPhaseEvents` currently
does in [ces_visibility.go](../../internal/app/ces_visibility.go).
Validate artifacts against a frozen projection and store snapshot; the worker
must not read a store that the control loop can change.

The controller must retain a handle to the fresh active phase session and its
cancel function. Current `PhaseRunner.Run` keeps that session local and closes
it on return. `handleAgentInterrupt` and `handleAgentSteering` address the
persistent roster sessions, while `handleGlobalSteering` only records input
during active CES work. Thus effective CES steering and cancellation require
new lifecycle plumbing. See [runner.go](../../internal/cognition/runner.go),
`Run`, and [update.go](../../internal/app/update.go), those three handlers.

### Commands and transport

Propose `ghost serve --socket <path>` and `ghost serve --stdio`.
Unix socket mode supports multiple clients. Stdio mode is a private child
process with one controlling client. Both use JSON-RPC 2.0: one UTF-8 JSON
value per line, with newlines inside strings escaped. Diagnostics go to stderr
or the existing diagnostic log, never the protocol stream.

Require request IDs for state-changing methods. Support JSON-RPC batches and
notifications according to the protocol; a notification has no response.
Reject mutation notifications without executing them and log the rejection.
Put limits on line size, batch size, request duration, and subscriber backlog.
Use 16 MiB as the initial line limit, matching the existing Codex reader's
`maxMessageBytes` in [client.go](../../internal/codex/client.go).
The new external protocol must validate `jsonrpc: "2.0"`; the existing
provider client is not itself a ready external server.

### Methods

| Method | Parameters | Result and rule |
| --- | --- | --- |
| `run.start` | `workspace`, `task`, `actor`, `request_key` | Return `run_id`, `work_id`, accepted state, and last `seq`. Validate configured workspace and available resources before acceptance. |
| `run.steer` | `run_id`, `text`, `request_key`, optional `expected_phase_run_id` | Return instruction ID and `delivery: live` or `queued`. Deliver to the fresh active session if it supports steering; otherwise apply at the next phase boundary. |
| `run.cancel` | `run_id`, `reason`, `request_key` | Return `canceling` or the existing terminal result. Stop further dispatch, interrupt the active turn, then close after a bounded wait. |
| `run.status` | `run_id` | Return run state, CES status/phase/reason, resource, session/turn, capabilities, queued instructions, last `seq`, and final result when available. |
| `qac.override` | `run_id`, `resource_id` or `clear: true`, `reason`, `request_key` | Set or clear a one-phase operator override. Apply only at the next allocation boundary. Return pending override and boundary. |
| `approval.respond` | `approval_id`, `decision` (`approve`\|`deny`), `responder`, optional `note`, `request_key` | Resolve a pending approval; see Approvals. Return the recorded decision. |
| `events.subscribe` | `run_id`, `after_seq` (default 0) | Return subscription ID and replay high-water mark; then send ordered `events.event` notifications. |

`request_key` is unique within the selected workspace for start, and within
the run for other mutations. Persist its request hash and result. Repeating
the same request returns the same result; reusing the key with changed input
is a conflict. Concurrent calls are ordered by the run control loop.
RPC acceptance does not mean the task or instruction has completed.

Use opaque run IDs, separate from CES work IDs and phase invocation IDs.
`Coordinator.Commit` currently generates its own `work-N` ID, while
`epistemic.NewStore` creates the CES task ID. Do not assume they are equal.
See [coordinator.go](../../internal/cognition/coordinator.go), `Commit`, and
[store.go](../../internal/epistemic/store.go), `NewStore`.

Steering is operator input, not evidence that an action succeeded. Store its
text and delivery record outside the phase artifact; include queued input in
the next request without silently changing the original goal. A stale expected
phase ID fails rather than steering a replacement phase.

An override must preserve resource availability, activation limits, cooldowns,
CES phase permissions, and approval policy. It must not interrupt a valid phase
to force a handoff. Record the operator reason and the resulting allocation.
This is a new operator control, not a model QAC request. Current allocation
uses `AllocateWithSituation`, `Snapshot`, and `Commit` in
[coordinator.go](../../internal/cognition/coordinator.go); there is no override
method there today.

### Event schema and resume

Propose this event envelope; IDs and time below are illustrative:

```json
{
  "schema_version": 1,
  "run_id": "run-7c81",
  "seq": 42,
  "time": "2026-10-12T10:15:30Z",
  "source": "CES",
  "kind": "phase_commit_succeeded",
  "message": "triage.commit_succeeded",
  "work_id": "task-91ab",
  "phase_run_id": "phase-e182",
  "session_id": "session-19c0",
  "turn_id": "turn-3",
  "meta": {
    "phase": "triage",
    "resource_id": "backend",
    "agent_id": "backend",
    "revision_id": "revision-63",
    "store_events": "3"
  }
}
```

The envelope extends `Writer.Write`'s time/source/kind/message/session/turn/meta
shape in [trace.go](../../internal/trace/trace.go). Preserve phase names from
[observe.go](../../internal/cognition/observe.go), the `Phase*` constants, and
correlation fields assembled by `cesRun.meta`, `traceCES`, and `phaseObserver`
in [ces_trace.go](../../internal/run/ces_trace.go). Optional IDs are absent
until known. `meta` remains a string map; typed store records use `data`.

Each socket notification is one NDJSON line with this envelope under params:

```json
{"jsonrpc":"2.0","method":"events.event","params":{"subscription_id":"sub-1","event":{"schema_version":1,"run_id":"run-7c81","seq":43,"time":"2026-10-12T10:15:31Z","source":"GHOST","kind":"run.finished","message":"work complete","meta":{"state":"complete"}}}}
```

Assign strictly increasing `seq` values per run, starting at 1, before durable
append and publication. Never reset them after reconnect or server restart.
Do not use timestamps for order. Replay events with `seq > after_seq`; establish
replay and live delivery under the same ordering boundary so no event is lost
between them. Clients deduplicate by `(run_id, seq)`.

Mirror controller, phase, runtime activity, QAC, and semantic CES events once;
do not publish the UI projection as a second copy. Current `recordProjection`
avoids a second trace write in [update.go](../../internal/app/update.go), and
`applyPhaseEvent` uses it in [ces_visibility.go](../../internal/app/ces_visibility.go).
Add run lifecycle, instruction delivery, override, and stream-loss events.

Keep transport sequence and store sequence separate. Include complete
`epistemic.Event` values in `kind: "ces.store_event"`, under `data.store_event`,
including Sequence, Kind, WorkID, ObjectKind, ObjectID, RevisionID, At, Payload.
Define a versioned wire mapping rather than rely on Go's default field names.
Capture newly appended records from `Store.Events()` after every store change,
including task creation and terminal changes, in store sequence order.
Do not assume `CommitResult.Events` supplies the canonical replay stream.
See [event.go](../../internal/epistemic/event.go) and
[store.go](../../internal/epistemic/store.go), `append`, `Events`, `commitPlan.appendTo`.

Current trace records contain commit counts and revision IDs, not complete
store payloads. Even verbose backend data is not a canonical CES event log.
To replay, select store records, decode their full payloads, and pass their
original consecutive store sequences to `epistemic.Replay`. It requires
sequences 1 through N and a task event. See [store.go](../../internal/epistemic/store.go),
`Replay`, and [ces_trace.go](../../internal/run/ces_trace.go), `traceCES`.

Journal complete mutation batches and their completion markers before exposing
their results. On recovery, discard an unfinished batch, validate sequence and
payload integrity, then replay. Event replay restores recorded CES state; it
does not prove that a shell command completed or restore a live backend session.
Initially mark formerly active runs interrupted and require new work; automatic
execution resume is a later feature. Never repeat a possible file write or shell
command merely because the server lost its connection.

For retained logs, reject an expired cursor with the earliest available sequence;
reject a cursor beyond the last sequence. A slow subscriber is disconnected
and must resume from its last received sequence. It must not slow the worker or
lose journal records. Current Codex `Session.emit`, RPC `deliver`, and fake
`Session.Emit` can drop buffered events; durable publication alone cannot
recover those upstream losses. Add explicit loss handling before claiming a
complete runtime stream. See [session.go](../../internal/codex/session.go),
[client.go](../../internal/codex/client.go), and [fake.go](../../internal/runtime/fake/fake.go).

### Errors, permissions, and concurrency

| JSON-RPC code | Meaning |
| --- | --- |
| -32700 | Invalid JSON. |
| -32600 | Invalid JSON-RPC request. |
| -32601 | Unknown method. |
| -32602 | Invalid parameters, unknown configured workspace/resource, or cursor beyond the end. |
| -32603 | Internal controller failure. |
| -32001 | Permission denied. |
| -32002 | Run not found. |
| -32003 | Conflict: workspace busy, stale phase, reused key, or invalid run state. |
| -32004 | Unsupported runtime capability or policy. |
| -32005 | Backend unavailable or startup failure. |
| -32006 | Cursor expired or journal unavailable for replay. |
| -32007 | Capacity or rate limit; include a retry delay when known. |
| -32008 | Control operation deadline exceeded; inspect status before retry. |

Error data should contain stable `reason`, `run_id` where known, and `retryable`.
Do not expose secrets, provider credentials, or unrestricted backend payloads.
Accepted run failures become terminal events and status, not delayed RPC errors.

Use socket file mode 0600 in an owner-only directory, mode 0700. Check ownership
and peer UID against the server UID on every connection. Use platform-specific
peer credential checks on Linux and macOS; fail closed if unavailable. Reject
symlink paths and do not remove an existing live socket. Only remove a stale
socket after verifying its type, owner, and lack of a live listener.
No network listener is proposed for Ghost's first server.

Same-UID access is the local trust boundary, not isolation from other processes
of that user. Stdio trusts the parent process and inherits its permissions.
Accept only configured workspace roots and configured resource IDs. RPC callers
cannot set arbitrary binary paths, environment values, prompts used as policy,
or broader file/network permissions. Backend approvals remain separate from
socket authentication. Current Codex approval requests are declined by
`rejectServerRequest` in [client.go](../../internal/codex/client.go).

Allow multiple runs across distinct workspaces, with one active run per canonical
workspace by default. Resolve symlinks and acquire a cross-process workspace
lock shared by serve and one-shot modes. Reject contention; do not queue silently.
Separate checkouts may run concurrently; serialize access to known shared
external resources through policy. A workspace lock does not isolate such resources.

Give each run its own store, coordinator, session registry, and runtime instance.
Current `Coordinator` owns one work item. Codex `Runtime.Start` replaces the
previous session for a repeated AgentID, and the fake runtime indexes by
AgentID. Sharing those maps across runs would cause collisions. See
[coordinator.go](../../internal/cognition/coordinator.go), `StartWork`,
[runtime.go](../../internal/codex/runtime.go), `Start`, and
[fake.go](../../internal/runtime/fake/fake.go), `Runtime.Start`.

Cancellation yields external state `canceled`, CES `incomplete`, and a recorded
operator reason. Add an explicit allowed CES cancellation reason: current
`validIncompleteReason` does not accept one in
[orchestration.go](../../internal/epistemic/orchestration.go).
Ignore late phase results after cancellation by run and phase invocation ID.
Do not report cancellation complete until session teardown finishes or a
bounded teardown failure is recorded.

### Approvals

Decided for v1. Each client declares an `actor` on `run.start`: an agent
name such as `"shade"`, or `"tui"` for the terminal UI. The actor is
recorded on the run and on every event that run emits.

When a runtime or Ghost tool needs approval (shell command, file write or
edit, network access, or anything outside the preauthorized policy), the run
pauses that action and emits an `approval.requested` event:

```json
{"schema_version":1,"run_id":"run-7c81","seq":57,"source":"GHOST","kind":"approval.requested",
 "data":{"approval_id":"appr-12","run_id":"run-7c81","actor":"shade","action_kind":"shell",
 "command":["go","test","./..."],"diff":null,"reason":"verify EXECUTE frame",
 "deadline":"2026-10-12T10:20:30Z"}}
```

`action_kind` is one of `shell`, `file_write`, `file_edit`, `network`, or
`other`; `command` or `diff` carries the exact action. A headless agent relays
the request to Mark in chat and calls `approval.respond` with `approval_id`,
`decision` (`approve` or `deny`), `responder`, and an optional `note`. The TUI
can answer the same request; the first valid response wins. Ghost emits
`approval.resolved` with the decision, responder, and note, then resumes or
skips the action.

Rules:

- No response by `deadline` means deny; Ghost emits `approval.resolved` with
  `responder: "ghost"` and `reason: "expired"`.
- Responses are idempotent: repeating the same decision for an approval
  returns the recorded result; a conflicting decision after resolution fails
  with -32003 and changes nothing.
- Every request and response is journaled with requester (run actor) and
  responder, and survives replay. Cancelling a run denies its pending approvals.
- A denial is reported to the runtime as a denied tool result, never as success.
- `qac.override` never grants tool authority.

Trust model: this is trust-based for v1. Any process running as the same UID
that can reach the socket can respond, and `responder` is self-declared, not
authenticated. It records accountability, not proof of identity.

Future hardening (not v1): out-of-band confirmation (TUI confirm or a
one-time code shown to Mark), signed responder identity per client, per-actor
policy on which action kinds need approval, and responder allow-lists.

### One-shot command

Propose `ghost run --task <text> --json`, with `--config` and `--workspace`.
Use the same controller, journal, workspace lock, and event schema. Stdout is
only NDJSON event envelopes, without RPC wrappers. Emit a final `run.finished`
event with state, CES reason, and exit code. Flush it before exit.

| Exit code | Meaning |
| --- | --- |
| 0 | CES complete, with successful stream/journal finalization. |
| 1 | Internal, backend, journal, or output delivery failure. |
| 2 | Invalid command or configuration. |
| 3 | CES incomplete or blocked by unresolved evidence/resource policy. |
| 4 | Workspace busy or server capacity reached. |
| 5 | Permission or approval denied. |
| 130 / 143 | Canceled by SIGINT / SIGTERM. |

If stdout closes, cancel and tear down the worker; do not leave unattended work.
If startup fails before run creation, write a structured diagnostic to stderr
and use the defined exit code. Current `main` has only success or exit 1 in
[main.go](../../cmd/ghost/main.go); these codes need a new dispatch boundary.

## 3. Runtime interface

### What exists

[internal/runtime/runtime.go](../../internal/runtime/runtime.go) already defines:

- `Runtime`: Name, Capabilities, Start(context, SessionConfig), Close.
- `Session`: ID, State, Send, Steer, Interrupt, Events, Stats, Metadata, Close.
- `SessionConfig`: agent, working directory, model, instructions, effort,
  sandbox and approval modes, plus optional MCP and Hooks values.
- `Input`: text plus an output schema. `Event`: time, agent/session/turn IDs,
  kind, summary/detail, metadata, and raw payload.
- Capability flags for steering, interrupt, resume, usage, tool and reasoning
  events. `Guard` serializes state and permits only one active turn per session.

The Codex adapter launches `codex app-server --stdio`, initializes it, starts
threads, and routes notifications by thread. `Send`, `Steer`, and `Interrupt`
use `turn/start`, `turn/steer`, and `turn/interrupt`. Close unsubscribes the
backend thread, removes its local route, stops the listener, and closes events;
it does not itself call turn interrupt. See [client.go](../../internal/codex/client.go),
`processFactory`, `route`; [runtime.go](../../internal/codex/runtime.go),
`Start`, `startThread`; and [session_lifecycle.go](../../internal/codex/session_lifecycle.go),
`Send`, `Steer`, `Interrupt`, `Close`.

Notification normalization supplies message, reasoning, command, file, tool,
usage, status, and error events. Rate-limit updates are merged into session
state and exposed through normalized metadata. This is not a generic quota
reservation service. See [notification.go](../../internal/codex/notification.go),
`normalizeNotificationPayload`, and [session.go](../../internal/codex/session.go),
`mergeRateLimits`, with [notification_details.go](../../internal/codex/notification_details.go),
`rateLimitMetadata`.

Codex currently advertises steering, interrupt, tool, and reasoning events;
it does not advertise resume or usage. There is no Resume method in the shared
interface, although a Resume flag exists. Session byte counts are not token
usage. See `Capabilities`, `SessionStats`, and Codex `Runtime.Capabilities`.

### Target contract

Keep the existing Runtime/Session split. Add only the fields and operations
needed to remove provider-specific assumptions:

- Give events explicit turn-started/completed/failed/canceled states, message
  delta/final markers, tool start/end IDs, usage units, and rate-limit fields.
  Preserve native method names separately for diagnostics.
- Replace `PhaseRunner.Run` checks for `backend_method == "turn/completed"`
  and substring checks for delta/completed messages with those shared markers.
  Today a new adapter must satisfy those Codex-shaped checks in
  [runner.go](../../internal/cognition/runner.go), `Run`, `isCompletedMessage`.
- Add capability checks for structured output, approvals, MCP, hooks, sandbox
  modes, and instruction delivery. Reject unsupported required policy before
  starting work; do not silently remove it.
- Make cancellation and Close bounded and idempotent. Context cancellation
  must stop provider work, not merely stop waiting. Emit terminal status once.
- Add a typed unsupported-operation error. Steer never means silently start a
  second turn; queue boundary instructions in the controller when unsupported.
- Add optional Resume(context, resume token, config) only after a provider
  contract is tested. Tokens stay distinct from Ghost run IDs. CES still uses
  a fresh session per phase; resume within a phase may support artifact repair.
- Preserve GHOST and phase instructions across adapters. Do not treat a
  provider's default prompt as a substitute for Ghost's contracts.

[internal/runtime/fake/fake.go](../../internal/runtime/fake/fake.go) is the
reference for the existing interface: `NewRuntime`, `NewSession`, Send, Steer,
Interrupt, Emit, and Close use `Guard` without a provider. Extend it with
scripted terminal events, capability limits, deadlines, and loss injection.
It is a lifecycle reference, not a real sandbox or proof of provider behavior.

## 4. Adapters

### a. Grok Build CLI

Local read-only checks used `grok --version`, `grok --help`, `grok agent --help`,
and help for agent stdio, serve, and leader. The installed build reports
`grok 1.0.50 (c58f321264ba) [stable]`. No model session or wire exchange was
started. CLI facts below come from that local help, not Ghost source.

| Verified option | Adapter use |
| --- | --- |
| `-p` / `--single <PROMPT>` | Run a single-turn prompt and exit. |
| `--prompt-file <PATH>` | Supply the single-turn prompt from a private file. |
| `--prompt-json <JSON>` | Supply JSON content blocks; not merely arbitrary task JSON. |
| `--output-format streaming-json` | NDJSON; one native ACP session update per line. |
| Other output formats | `plain`, `json`, `streaming-messages-json`; the last supports `--include-partial-messages` for text/thinking deltas. |
| `-s` / `--session-id <UUID>` | Name a new conversation; UUID must not already exist. |
| `-r` / `--resume <id>` | Continue a conversation; use its exact ID, not title or most-recent selection. |
| `--cwd`, `-m` / `--model`, `--reasoning-effort` | Select directory, model, and effort. |
| `--max-turns` | Bound the provider's agent turns, separately from CES phases and repair turns. |
| `--permission-mode` | Values: `default`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`, `plan`. |
| `--sandbox <PROFILE>` | File/network profile; environment fallback is `GROK_SANDBOX`. |
| `--always-approve`, `--allow`, `--deny` | Approval bypass or permission rules. |
| `--tools`, `--disallowed-tools` | Select/remove built-in tools. |
| `--rules`, `--system-prompt-override` | Append rules or replace the provider system prompt. |
| `--json-schema` | Constrain output; help says it implies `--output-format json`. |
| `--no-subagents` | Disable provider subagent spawning. |

Start with the single-turn CLI adapter and recorded `streaming-json` fixtures.
It has a documented line format and explicit noninteractive invocation.
Use a new UUID per CES phase; use `--resume <UUID>` only for a later repair
turn in that same phase. Keep the logical Session open across child processes.
Propose initial steering=false, interrupt=true after process teardown tests,
resume=false until continuation is verified. Treat usage/tool/reasoning flags
as unconfirmed until fixture coverage establishes them.

Pass arguments as an argument array, never through a shell. Prefer private
prompt files to avoid large prompts in process arguments, and remove them on
Close. Give each process a bounded stderr capture and managed process group.
On interruption, stop its children too; a killed parent is not proof that tools
stopped. Require a verified response end plus successful process exit before
emitting turn completion. EOF alone is not success.

Preserve the authoritative final artifact without duplicating streamed text.
Test `--json-schema` with explicit streaming output before combining them:
help's implied JSON format leaves their interaction unresolved. Until verified,
use the phase instructions and Ghost's parser/validator/repair loop, or reject
a configuration that requires native schema enforcement. `PhaseRunner.Run`
already applies `PhaseOutputSchema`, parses artifacts, and permits two repair
attempts in [runner.go](../../internal/cognition/runner.go).

Do not equate `ApprovalNever` with `--always-approve`; no approval request can
mean denial, whereas that flag grants execution. Map policy deliberately and
verify the effective sandbox profile. Do not invent profile names or assume
permission modes enforce an operating-system sandbox. Keep `--no-subagents`
on initially so Ghost remains the resource allocator. Use appended rules first;
system-prompt replacement needs a tested mapping for Ghost and provider rules.

Investigate these alternatives after the first adapter:

- `grok agent stdio`: documented as agent over stdio. Likely ACP JSON-RPC,
  but help does not establish initialization, framing, cancellation, approval,
  or session methods. Capture a controlled exchange before claiming parity.
  If confirmed, this is the preferred long-term transport because it can
  support a persistent bidirectional session like Codex's app-server client.
  Reuse transport concepts, not Codex method names or its approval rejection.
- `grok agent serve`: WebSocket server, default `127.0.0.1:2419`, with a secret
  option or `GROK_AGENT_SECRET`; help says a secret is generated if absent.
  Adds listener authentication and reconnect handling; defer until needed.
- `grok agent leader`, `--leader`, `--leader-socket`: shared backend;
  default socket `~/.grok/leader.sock`. Agent help says leader selection also
  follows `[cli] use_leader`; use `--no-leader` for an isolated stdio probe.
  Verify session separation, permissions, ownership, and cancel scope before
  sharing a leader. Closing one Ghost run must not kill other clients.

There is no Grok adapter now: `config.validate` only accepts codex, and
`Model.New`/`Init` construct Codex directly in
[config.go](../../internal/config/config.go) and [app.go](../../internal/app/app.go).

### b. OpenAI-compatible API

Propose `openai-compat` with chat completions and tool calls, configured through
`base_url`, `api_key_env`, and `model`. This name is already recognized as
unimplemented by `config.validate` in [config.go](../../internal/config/config.go).
Compatibility must be tested per endpoint; the name alone guarantees no
structured-output, reasoning, usage, or tool-call streaming behavior.

Unlike Codex's app-server or Grok Build, this adapter must supply the agent
execution loop: send messages, assemble complete streamed tool calls, validate
arguments, run tools, return tool results, and continue until a final response
or limit. Tool output is data, never policy instructions. Bound requests,
tool turns, output sizes, elapsed time, and retries. Never retry a tool with
side effects automatically after an uncertain completion.

Ghost must provide read_file, write_file, edit_file, shell, and search tools.
Check canonical paths and symlinks against allowed roots. Use atomic file
writes and expected-content checks for edits. A path check cannot contain shell
commands: shell requires an operating-system sandbox with explicit file and
network permissions, child-process cleanup, and bounded output capture.
Emit tool lifecycle events with stable IDs and outcomes for CES observation.

Approval-gated tools use the approval flow in section 2, "Approvals". Until
that ships, the first adapter allows only preauthorized tools and rejects all
other operations.
Current `runtime.Session` has no approval response method, and Codex declines
backend requests in [client.go](../../internal/codex/client.go), `rejectServerRequest`.

Read the API key from the named environment variable at runtime. Keep it out
of prompts, traces, errors, and status. Preserve initial and phase instructions
in explicit API messages. Advertise capabilities only after tests against the
selected endpoint; do not expose unavailable reasoning content as if received.

## 5. Configuration changes

Current `Config` has ui, global, agents, memory, safety, trace, and qac tables.
`BackendConfig` uses runtime/model/effort/working_dir, MCP/hooks, sandboxed,
ghost_mode, and soul_prompt. `decode` rejects unknown fields; `validate` resolves
directories and prompt files relative to the configuration file. Effort accepts
low/medium/high/xhigh/max/ultra. See [config.go](../../internal/config/config.go),
those types, `decode`, `validate`, and `resolvePath`.

Keep those names and QAC resource-to-agent mappings. Add typed `HeadlessConfig`
and provider-specific backend option structs. Extend runtime validation only
when an adapter can start. Reject contradictory options and unsupported
provider effort values early. Resolve new paths against the config directory.
Do not pass arbitrary configuration keys to provider command arguments.

This example uses only existing settings and preserves an explicit sandbox:

```toml
[global]
initial_prompt = "GHOST.md"

[agents.backend]
runtime = "codex"
working_dir = "."
sandboxed = true
ghost_mode = false

[qac]
enabled = true
entry_resource = "backend"
default_importance = 0.5

[qac.policy]
type = "threshold"
hierarchy = ["backend"]

[qac.resources.backend]
agent = "backend"
capability = 0.5
cost = 0.5
scarcity = 0.5
```

The existing [ghost.toml.example](../../ghost.toml.example) sets only a Codex
backend and optional trace comments; it does not enable QAC. The current CES
start path requires a coordinator. For headless CES, require valid QAC settings
initially; do not silently run the legacy direct-agent path.
See `handleGlobalSteering` and `startCESWork` in
[update.go](../../internal/app/update.go) and [ces.go](../../internal/run/ces.go).

Proposed additions below are rejected by today's strict decoder. These are
future examples for ghost.toml and ghost.toml.example, not instructions to
edit either file as part of this note:

```toml
[headless]
socket_path = ".ghost/serve.sock"
journal_dir = ".ghost/runs"
max_active_runs = 4
max_active_per_workspace = 1
max_message_bytes = 16777216
subscriber_queue = 256
operation_timeout = "30s"
cancel_timeout = "10s"
retention = "7d"

[trace]
path = ".ghost/trace.jsonl"
verbose = false
stall_after = "10m"
```

Use configured agent working directories as the allowed workspace set.
Socket permissions and peer UID checks are mandatory, not configurable off.
The replay journal is mandatory and separate from the optional diagnostic
trace. Complete store payloads remain private even when trace verbosity is off.
For multiple runs, partition trace output by run or include run IDs throughout.
Check existing file permissions too: `trace.Open`'s creation mode does not
change permissions on an already existing file in [trace.go](../../internal/trace/trace.go).

Alternative proposed agent tables follow. Pair the selected agent with the QAC
resource mapping above; these options do not imply both must be active.

```toml
[agents.backend]
runtime = "grok"
working_dir = "."
sandboxed = true
ghost_mode = false

[agents.backend.grok]
binary = "grok"
transport = "single"
max_turns = 20
permission_mode = "default"
no_subagents = true
# sandbox_profile = "<verified local profile>"
```

Add `GrokConfig` fields for sandbox_profile, allow/deny rule arrays, and allowed
or removed tools. Require a verified profile when sandboxed=true. Do not infer
one from an environment fallback without checking the effective policy.
Keep MCP/hooks rejected until supported. The existing makeSessionConfig maps
sandboxed=false to SandboxDisabled and ghost_mode=true to ApprovalNever in
[app.go](../../internal/app/app.go); document this and require explicit safe
headless policy rather than silently inherit an unrestricted default.

```toml
[agents.backend]
runtime = "openai-compat"
model = "provider-model-id"
working_dir = "."
sandboxed = true
ghost_mode = false

[agents.backend.openai_compat]
base_url = "https://provider.example/v1"
api_key_env = "GHOST_PROVIDER_API_KEY"
max_tool_turns = 20
request_timeout = "2m"
tools = ["read_file", "search"]
```

Add `OpenAICompatConfig` and a separate tool-policy configuration for write/edit,
shell, network, and approvals. Validate URL, key-variable name, limits, tool
names, and durations. Use HTTPS except explicitly authorized local endpoints.
The model ID is illustrative, not a supported model claim. Existing QAC budget
settings remain `max_activations_per_run` and `cooldown` under each resource's
budget table; do not rename them. See `QACBudgetConfig` and `validateQAC` in
[config.go](../../internal/config/config.go).

## 6. Phased plan and tests

Each item is one small PR, or split further if needed. These are proposed tests;
this design-only change does not run or claim them.

1. Extract the UI-free controller without changing CES decisions. Use fake
   runtime scripts for full phases, repairs, repeat limits, terminal results,
   and trace failure. Keep TUI behavior checks. Start from existing
   [ces_trace_test.go](../../internal/app/ces_trace_test.go),
   `TestTraceRecordsCompletePhaseChain`, and [runner_test.go](../../internal/cognition/runner_test.go).
2. Add typed runtime lifecycle/message/tool events and required capabilities.
   Update Codex and fake together. Preserve delta-only, final-only, and combined
   capture behavior from [runner_capture_test.go](../../internal/cognition/runner_capture_test.go).
   Test unsupported policy, event loss, close races, backend exit, and late turns.
3. Add per-run durable journals and sequence-based subscriptions. Use golden
   event streams with deterministic IDs and time. Replay full store records
   through `epistemic.Replay` and compare state. Test gaps, duplicates, corrupt
   payloads, unfinished batches, restart, cursor expiry, and replay/live overlap.
4. Add one-shot command dispatch and exit codes. Test stdout purity, final flush,
   no terminal requirement, broken output pipes, signal cancellation, workspace
   locks, and config errors with an injected fake runtime.
5. Add Unix socket/stdio JSON-RPC and the six core methods. Test request and
   batch parsing, IDs, errors, permissions, peer UID checks, stale sockets,
   duplicate keys, slow subscribers, and parallel runs in separate workspaces.
6. Add active-phase steering/cancel, boundary QAC overrides, and approvals
   (`actor` on `run.start`, `approval.requested`/`approval.resolved` events,
   `approval.respond`, TUI answer path). Test deadline expiry as deny,
   idempotent and conflicting responses, cancel with pending approvals, and
   journal replay of requests and responses. Test cancel during
   startup, tools, repair, and commit; late results cannot complete canceled work.
   Verify availability, budgets, cooldowns, and completion gates remain enforced.
7. Add Grok single-turn adapter and typed config. Use sanitized recorded NDJSON
   fixtures for chunks, final output, tool updates, malformed lines, failure,
   EOF, repair resume, and schema-format interaction. Mark inferred behavior
   separately. Run a bounded opt-in real CLI check; fixtures do not prove it.
8. Probe Grok stdio in a separate PR. Record initialization, session, prompt,
   cancel, and permission exchanges before implementing the transport. Defer
   WebSocket/leader support until isolation and disconnect tests pass.
9. Add OpenAI-compatible read-only tool loop, then sandboxed write/shell tools
   and approval responses in a separate PR. Test streamed tool arguments,
   bad arguments, symlink escape, edit conflicts, denied/expired approvals,
   process cleanup, secret removal, limits, and uncertain side-effect results.

Run focused package tests at each step and `go test -race` for the controller,
runtime, Codex, fake, journal, and transport packages. Before integration, run
`go test ./...` and `go test -race ./...`. Existing routing and teardown tests in
[routing_test.go](../../internal/codex/routing_test.go) and
[fake_test.go](../../internal/runtime/fake/fake_test.go) supply regression cases.
Report separately: fake tests, fixture tests, local sandbox tests, and real
provider checks. None substitutes for the others.

## 7. Open questions

- Is queued steering sufficient for the first release, or must Grok provide
  live steering before Mark's teammates can use it?
- What exact framing and ACP methods does Grok agent stdio implement? Can it
  enforce the required sandbox and handle approval replies without terminal input?
- Can Grok schema enforcement coexist with streaming-json, and what marks an
  authoritative final artifact and successful completion?
- Which local Grok sandbox profiles meet Ghost's file and network policy?
  What subprocesses survive parent interruption or a shared-leader disconnect?
- Which OpenAI-compatible endpoint is first, and which structured-output,
  tool-call, usage, and rate-limit fields does it actually support?
- Resolved for v1: who may answer approvals. Any same-UID client or the TUI
  may respond; agents relay requests to Mark in chat. Trust-based; see
  "Approvals" for future hardening.
- How long should full CES journals be kept, and what sensitive evidence must
  be removed while preserving enough payload for replay?
- Should interrupted runs later resume within a phase, or always start fresh
  after recorded evidence and workspace changes are checked?
- Should resource availability later stop depending on persistent idle sessions?
  Current `Coordinator.Snapshot` uses those handles; replacing them needs a
  tested health/capacity contract, not removal during controller extraction.
- Are cross-process workspace locks enough, or do shared services need explicit
  reservations? Should operator overrides ever have a longer lifetime than one phase?
