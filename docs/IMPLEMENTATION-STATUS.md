# Ghost Next implementation status

Status: Milestone B steps 1 and 2 are implemented. Step 2 uses the controller
projection for the default Bloodwire conversation and event inspector. The
acceptance review below records one partial item: agent tabs use alphabetical
order because `config.AgentSet` is a map and does not retain TOML source order.

| Status | Area | Evidence |
| --- | --- | --- |
| Implemented and tested | `internal/core` contracts | IDs, Agent, Task, Message, Event, sequencing, and JSONL log |
| Implemented and tested | Runtime event mapping | `internal/run/event_adapter.go`; source payloads remain JSON |
| Implemented and tested | Workspace projection | Agents, tasks, messages, event queries, and controller snapshots |
| Implemented and tested | Controller API | `Start`, `StartWithPolicy`, `Steer`, `Send`, `Cancel`, and policy control |
| Implemented and tested | Durable event log | Private per-run files under the selected work directory; fail-soft errors |
| Implemented and tested | CES policy | Configured-QAC default, task override, pending policy, standard path when off |
| Implemented and tested | Bloodwire conversation TUI | `internal/app/conversation*.go`; projection-driven rendering, commands, focus, task view, and event panel |
| Partial | Agent tab order | Tabs and overflow work; alphabetical ordering is used because `AgentSet` has no source-order field |
| Partial | Headless runtime | `ghost serve` logs under the selected workspace and reports generic task IDs and states; no workspace or CES-control RPC methods |
| Not done | Provider adapters | Codex remains the only configured production provider |
| Not done | Runtime recovery | Logs replay, but agents, tasks, messages, and provider sessions are not rebuilt |
| Not done | Permissions, memory, and retryable message delivery | Contracts or design exist; runtime enforcement is not part of this step |

## Bloodwire acceptance review

| # | Status | Evidence |
| --- | --- | --- |
| 1 | Implemented and tested | Default conversation view uses the controller projection; activity text, ASCII goldens, `TestFullAppWindowRenderShowsProjectionAndComposer`, `TestAgentActivityAndPerTabScrollState` |
| 2 | Partial | Shared and agent tabs, broadcasts, task/activity filtering, overflow selector; `TestAgentTabShowsItsMessagesAndBroadcasts`, `TestPaletteSelectsOverflowAgentAndLegacyReturns`. Order is alphabetical, not TOML source order. |
| 3 | Implemented and tested | Structured task, tool, QAC, CES, and failure rows; `TestConversationSummarizesSignificantStructuredEvents`, golden views |
| 4 | Implemented and tested | Side/overlay inspector, filter, category, search, follow, raw payload; `TestEventsPanelFiltersSearchesFollowsAndExpands`, `TestEventFollowPauseKeepsSelection`, golden views |
| 5 | Implemented and tested | Composer routing, commands, input focus, cancel, and key bindings; `TestComposerUsesTabDefaultsAndMentionOverrides`, `TestConversationKeyboardAndComposerFocus`, `TestHelpAndQuitComposerCommands`, `TestNewViewSendsAndCancelsThroughFakeRuntime` |
| 6 | Implemented and tested | Filterable command/agent palette; `TestPaletteSelectsOverflowAgentAndLegacyReturns` |
| 7 | Implemented and tested | Controller-backed CES request, unsupported and pending labels; `TestCESControlsUseControllerStatus`, CES goldens |
| 8 | Implemented and tested | Task detail fields and return selection; `TestTaskDetailOpensFromConversationSelection` |
| 9 | Implemented and tested | Old dashboard remains available through `:legacy`; same palette/legacy test |
| 10 | Implemented and tested | Cached row rendering and 5,000-event timing/focus check; `TestRenderLargeEventHistoryAndKeepFocusStable` |
| 11 | Implemented and tested | No-color goldens at 80×24, 100×30, and 140×40; `TestBloodwireGoldenViews` |

## Verification results

| Command | Result |
| --- | --- |
| `gofmt -l .` | Pass; no output |
| `go vet ./...` | Pass |
| `go build ./...` | Pass; used a writable module cache under `/tmp` |
| `go test -race -count=1 ./...` | All packages pass except `internal/serve/TestSocketAcceptsTheSameUser`; the sandbox blocks its Unix socket bind with `operation not permitted` |

No test was changed for the socket sandbox failure. The TUI tests use the fake
runtime and controller projection; they do not verify a live provider.

CES and QAC are separate systems. CES owns phase order, epistemic state, and
work status. QAC selects a resource for a CES phase. The default remains CES
on when valid QAC configuration is present. See
[`design/runtime.md`](design/runtime.md) and
[`design/communication.md`](design/communication.md).

## Known gaps

- The workspace projection is in memory. It does not recover from the event
  log after restart.
- Agent tabs use alphabetical order until configuration stores an explicit
  order.
- `ghost serve` keeps its current RPC methods. It does not expose the new
  workspace snapshot or policy methods.
- Configured agents should share one workspace root in the TUI. If roots
  differ, the first agent by name selects the TUI event-log root. `ghost serve`
  uses the workspace named by each run request.
