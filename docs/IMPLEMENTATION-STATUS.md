# Ghost Next implementation status

Status: Milestone B step 1 is implemented. This covers runtime state and
events for the later TUI step. The current TUI remains in place.

| Status | Area | Evidence |
| --- | --- | --- |
| Implemented and tested | `internal/core` contracts | IDs, Agent, Task, Message, Event, sequencing, and JSONL log |
| Implemented and tested | Runtime event mapping | `internal/run/event_adapter.go`; source payloads remain JSON |
| Implemented and tested | Workspace projection | Agents, tasks, messages, event queries, and controller snapshots |
| Implemented and tested | Controller API | `Start`, `StartWithPolicy`, `Steer`, `Send`, `Cancel`, and policy control |
| Implemented and tested | Durable event log | Private per-run files under the selected work directory; fail-soft errors |
| Implemented and tested | CES policy | Configured-QAC default, task override, pending policy, standard path when off |
| Designed only | Milestone B step 2 TUI | [`design/tui.md`](design/tui.md) |
| Partial | Headless runtime | `ghost serve` logs under the selected workspace and reports generic task IDs and states; no workspace or CES-control RPC methods |
| Not done | Provider adapters | Codex remains the only configured production provider |
| Not done | Runtime recovery | Logs replay, but agents, tasks, messages, and provider sessions are not rebuilt |
| Not done | Permissions, memory, and retryable message delivery | Contracts or design exist; runtime enforcement is not part of this step |

CES and QAC are separate systems. CES owns phase order, epistemic state, and
work status. QAC selects a resource for a CES phase. The default remains CES
on when valid QAC configuration is present. See
[`design/runtime.md`](design/runtime.md) and
[`design/communication.md`](design/communication.md).

## Verification results

| Command | Result |
| --- | --- |
| `gofmt -l .` | Pass; no output |
| `go vet ./...` | Pass |
| `go build ./...` | Pass; used a writable module cache under `/tmp` because the default cache is outside the sandbox write roots |
| `go test -race -count=1 ./...` | All packages pass except `internal/serve/TestSocketAcceptsTheSameUser`; the sandbox blocks its Unix socket bind with `operation not permitted` |

No test was changed for the socket sandbox failure.

## Known gaps

- The workspace projection is in memory. It does not recover from the event
  log after restart.
- The TUI does not yet read the new workspace projections or expose CES
  policy controls. That is milestone B step 2.
- `ghost serve` keeps its current RPC methods. It does not expose the new
  workspace snapshot or policy methods.
- Configured agents should share one workspace root in the TUI. If roots
  differ, the first agent by name selects the TUI event-log root. `ghost serve`
  uses the workspace named by each run request.
- Tests do not prove behaviour against a live Codex app-server.
