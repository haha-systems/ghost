# Ghost Next implementation status

Status: This records which Ghost Next work exists in this checkout. The audit
is a completed documentation artifact; test status applies to executable code.
Results below were run on 2026-10-25 (macOS arm64, Go 1.26.8) outside any sandbox.

Completed audit: [`phase0-audit.md`](audit/phase0-audit.md). It cites source
paths and symbols.

| Status | Area | Evidence |
| --- | --- | --- |
| Implemented+tested | `internal/core` contracts | IDs, Agent, Task, Message, Event, sequencer, and JSONL log |
| Partial | Runtime-owned multi-agent state | Configured agents, runtime sessions, QAC work, and CES state remain separate |
| Partial | Provider adapters | Codex only; Claude and OpenAI-compatible adapters are not present |
| Designed only | Phase B TUI | [`design/tui.md`](design/tui.md) |
| Designed only | Runtime tasks, messages, addressing, and permissions | [`design/runtime.md`](design/runtime.md) and [`design/communication.md`](design/communication.md) |
| Designed only | Memory and decision event wiring | [`design/memory.md`](design/memory.md) and [`design/decisions.md`](design/decisions.md) |
| Designed only | Runtime integration of `internal/core` | No existing package imports `internal/core` |
| Blocked | None | Phase A needs no external service |

## Verification results



| Command | Result |
| --- | --- |
| `gofmt -l .` | pass (no output) |
| `go vet ./...` | pass |
| `go build ./...` | pass (also `make build`, `make test`) |
| `go test -race -count=1 ./...` | pass: all 22 packages with tests `ok` (event, logging, model have no tests); `internal/core` also passes `-race -count=3` |

The `internal/core` tests cover ID ordering and validation; agent, task, and
message rules; task transitions; event category and JSON handling; concurrent
stream sequencing; and JSONL append, replay, and recovery. These contracts are
not wired into `run`, `serve`, or `app`. The existing
[headless runtime proposal](design/headless-and-runtimes.md) and
[CES guide](CES.md) remain the references for those systems.

## Known gaps (milestone A)

- `internal/core` is a contract layer only: nothing in `run`, `serve`, `app` or
  `codex` produces or consumes `core.Agent`, `core.Task`, `core.Message` or
  `core.Event` yet. Existing `runtime.Event` / trace JSONL remain the live path.
- `core.EventLog` is tested in isolation (append, replay, truncated-tail
  recovery, mid-file corruption rejected); it is not used for any real state, so
  no existing persisted data is migrated or at risk.
- No mapping exists yet from `runtime.Event` (Codex notifications) to
  `core.Event`; that adapter is the first step of milestone B.
- Provider behaviour was not exercised against a live Codex app-server in this
  milestone; only the existing unit tests (fake runtime) were run.
