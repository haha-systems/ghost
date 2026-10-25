# Ghost Next implementation status

Status: Milestone C, Steps 1–3 are implemented and tested with the fake runtime. Step 1 delivered the in-memory broker and strict channel parser. Step 3 restores conversation history and reconciles interrupted work. No live-provider behavior is claimed.

Milestone D Step 3 adds runtime memory recall and capture, strict agent memory
blocks, memory event projection, and lost-write recovery. Fake runtime and
memory backend tests cover these paths. Live Ghostdive behavior and the TUI
memory inspector remain unverified or not implemented.

| Status | Area | Evidence |
| --- | --- | --- |
| Implemented and tested | `internal/core` contracts | IDs, Agent, Task, Message, Event, sequencing, and JSONL log |
| Implemented and tested | Runtime event mapping | `internal/run/event_adapter.go`; source payloads remain JSON events |
| Implemented and tested | Controller communication wiring | One `comms.Router`; session delivery through `Send` and `Steer`; cancellation through `CancelTask` |
| Implemented and tested | Message and task projection | Workspace copies router records; accepted and delivery events share the core event log |
| Implemented and tested | Agent channel | Final session messages use `ParseAgentChannel`; sender identity comes from the session; invalid requests are visible and bounded |
| Implemented and tested | Fake runtime | Scriptable `Session.OnSend` and `Session.OnSteer`; default behavior remains unchanged |
| Implemented and tested | Bloodwire conversation TUI | Agent-to-agent messages, delivery states, coordination cards, child task detail, and task cancellation |
| Partial | Child CES execution | Child policy is inherited and recorded. Child work uses the standard session path; separate CES orchestration is not supported |
| Partial | Permissions | Broker checks message authority and intersects child permission policies; runtime tool enforcement remains outside this broker |
| Partial | Headless runtime | `ghost serve` logs under the selected workspace and reports generic task IDs and states; no workspace or CES-control RPC methods |
| Implemented and tested | Restart recovery | Read-only replay, router rehydration, task and delivery reconciliation, bounded history, and TUI rendering; see Scenario 8 tests below |
| Not done | Additional production providers | Codex remains the only configured production provider |

## Phase 2 acceptance scenarios

| # | Status | Evidence |
| --- | --- | --- |
| 1. Human direct message reaches only its recipient | Implemented and tested | `TestControllerBrokerHumanDirectMessageStartsOnlyRecipient` |
| 2. Broadcast uses one ID and per-recipient delivery | Implemented and tested | `TestControllerBrokerHumanBroadcastSharesMessageIDAndDeliversAll` |
| 3. Agent direct message uses authenticated sender and framed delivery | Implemented and tested | `TestControllerBrokerRoutesAgentMessageFromAuthenticatedSender` |
| 4. Delegation creates a linked child with inherited CES and intersected permissions | Implemented and tested | `TestControllerBrokerDelegationReturnsResultToParent` |
| 5. Child result completes the task and reaches the parent session | Implemented and tested | `TestControllerBrokerDelegationReturnsResultToParent` |
| 6. Task cancel cascades and interrupts active child owners | Implemented and tested | `TestControllerBrokerCancelTaskCascadesAndInterruptsOwners`; `TestCancelCommandPrefixAndTaskDetailKey` |
| 7. Offline delivery stays pending or fails with a visible reason | Implemented and tested | `TestControllerBrokerOfflineDeliveryFailureIsVisible` |
| 8. Message history survives a TUI restart | Implemented and tested | `TestControllerRestartRestoresHistoryAndRetriesOnlyPurePendingMessages`; `TestRouterRehydrateHasNoDeliverySideEffectsAndReconcilesUncertainWork`; `TestRouterRehydrateRestoresConsultationsBudgetsAndDuplicateKeys`; `TestReadWorkspaceEventHistoryHandlesEmptyTailAndMiddleCorruption`; `TestReadWorkspaceEventHistoryCapsFilesAndEvents`; `TestRecoveredHistoryRendersInAllAndAgentTabs` |
| 9. Delegation cycles and message ping-pong stop at limits | Implemented and tested | `TestControllerBrokerRejectsCyclicDelegation`; `TestControllerBrokerMessageBudgetStopsPingPong` |
| 10. Messages and events are inspectable without duplicating provider telemetry | Implemented and tested | `TestControllerBrokerEventsAndMessagesHaveSingleSource` |

Additional channel checks cover bare mentions, sender spoofing, and rejection notice limits in `TestControllerBrokerIgnoresBareMentionsAndRejectsSenderSpoofing`. Capability brief delivery is checked in `TestControllerBrokerCapabilityBriefIsInSessionInstructions`. The TUI renders the delegation, result, delivery state, parent link, and both agent tabs in `TestDelegationAndResultCardsRenderForBothAgents`.

## Bloodwire acceptance review

| # | Status | Evidence |
| --- | --- | --- |
| 1 | Implemented and tested | Shared conversation view; `TestFullAppWindowRenderShowsProjectionAndComposer`, `TestAgentActivityAndPerTabScrollState` |
| 2 | Implemented and tested | Shared and agent tabs, broadcasts, task/activity filtering, overflow selector; `TestAgentTabShowsItsMessagesAndBroadcasts`, `TestPaletteSelectsOverflowAgentAndLegacyReturns` |
| 3 | Implemented and tested | Structured task, tool, QAC, CES, failure, and communication rows; `TestConversationSummarizesSignificantStructuredEvents`, golden views |
| 4 | Implemented and tested | Side/overlay inspector, filter, category, search, follow, raw payload; `TestEventsPanelFiltersSearchesFollowsAndExpands`, golden views |
| 5 | Implemented and tested | Composer routing, commands, input focus, cancel, and key bindings; `TestComposerUsesTabDefaultsAndMentionOverrides`, `TestConversationKeyboardAndComposerFocus` |
| 6 | Implemented and tested | Filterable command/agent palette; `TestPaletteSelectsOverflowAgentAndLegacyReturns` |
| 7 | Implemented and tested | Controller-backed CES request, unsupported and pending labels; `TestCESControlsUseControllerStatus`, CES goldens |
| 8 | Implemented and tested | Task detail includes parent, children, and related descendant messages; `TestTaskDetailOpensFromConversationSelection`, `TestDelegationAndResultCardsRenderForBothAgents` |
| 9 | Implemented and tested | Former dashboard remains available through `:legacy`; palette/legacy test |
| 10 | Implemented and tested | Cached row rendering and 5,000-event timing/focus check; `TestRenderLargeEventHistoryAndKeepFocusStable` |
| 11 | Implemented and tested | No-color goldens at 80×24, 100×30, and 140×40; `TestBloodwireGoldenViews` |

## Verification results

Run by the operator on 2026-10-25 (macOS arm64, Go 1.26.8), outside any sandbox, at the tip of milestone C:

| Command | Result |
| --- | --- |
| `gofmt -l .` | Pass; no output |
| `go vet ./...` | Pass |
| `make build test vet` | Pass |
| `go test -race -count=1 ./...` | Pass; every package with tests `ok`, including `internal/serve` |
| `go test -race -count=6 ./internal/{run,comms,core,app}` | Pass; no flakes |

**Live provider: not done.** No live Codex session was run for milestone C.
Ghost's config can't request a read-only Codex sandbox: `sandboxed = true` maps
to `workspace-write`, and the default (`sandboxed = false`) maps to
`danger-full-access` (`internal/codex/runtime.go`). So the
read-only/tiny-task condition for a live run could not be met. Fake sessions
prove the controller behaviour and message framing only. In particular,
nothing has verified that a real Codex model reliably emits well-formed
`ghost-*` blocks.

CES and QAC are separate systems. CES owns phase order, epistemic state, and work status. QAC selects a resource for a CES phase. See [`design/runtime.md`](design/runtime.md), [`design/communication.md`](design/communication.md), and [`design/tui.md`](design/tui.md).

## Known gaps

- Provider turns do not survive process restart. Unfinished tasks fail with `interrupted: ghost restarted`; this milestone does not resume or verify a live provider turn.
- Recovery reads at most 100 files and 20,000 events by default. Older history is omitted and reported in the recovery summary.
- Child CES policy is inherited, but child tasks use the standard session path.
- `ghost serve` does not expose workspace snapshot or CES-control RPC methods.
- Configured agents should share one workspace root in the TUI. If roots differ, the first agent by name selects the TUI event-log root. `ghost serve` uses the workspace named by each run request.
- The agent channel is a text protocol: fenced `ghost-*` JSON blocks in an agent's final message. The sender is authenticated as the agent's session, but there is no Codex tool/MCP bridge yet. Delivery to agents is framed text through `Send`/`Steer`.
- The broker computes permission intersections, but they are not enforced at the provider tool level. Codex still runs with whatever sandbox the config selects.
- Codex sandbox config: there is no read-only option, and the default is `danger-full-access`. This predates Ghost Next and should be fixed before live multi-agent runs.
