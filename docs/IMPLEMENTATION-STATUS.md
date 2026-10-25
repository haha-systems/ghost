# Ghost Next implementation status

Milestone C, Steps 1–3, remains implemented and tested with the fake runtime.
Step 1 delivered the in-memory broker and strict channel parser. Step 3 restores
conversation history and reconciles interrupted work. No live-provider
behavior is claimed.

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

## Milestone D: Ghostdive memory with provenance

Memory is off unless `[memory] enabled = true` and a valid Ghostdive
`space_id` is configured. With memory off, agent input is byte-identical
(`TestControllerDisabledMemoryKeepsHumanInputUnchanged`). The table follows the
Phase 3 acceptance criteria in GHOST-NEXT-PLAN.

| # | Phase 3 acceptance criterion | Status | Evidence |
| --- | --- | --- | --- |
| 1 | New tasks automatically receive relevant memory | Implemented and tested (fake backend) | `TestControllerMemoryContextArrivesBeforeHumanTaskSend`; relevance ranking is Ghostdive's and is not verified live |
| 2 | Managed and native agents receive context through supported mechanisms | Partial | Managed (Codex): the context package is prepended to the input Ghost sends at task start or delegation, never mid-turn. There are no native agents in this repo. |
| 3 | Agents can access authorised memories from other agents | Implemented and tested | `TestRecallReturnsRelevantWorkspaceMemoryToAnotherAgent`; `TestServiceOverHTTPAdapterStoresAndRecallsAcrossAgents`; `TestControllerDoesNotInjectAnotherAgentsPrivateMemory`. Isolation inside a shared space is client-side (see below). |
| 4 | Delegation supplies relevant shared context | Implemented and tested | `TestControllerDelegatedChildReceivesSharedWorkspaceMemory` |
| 5 | Significant task results can be retained | Implemented and tested | `TestControllerCompletedTaskQueuesInferenceMemoryAndPersistsEvents`; `TestControllerAgentRememberAndChallengeQueueLinkedWrites`. Results are stored as unverified inferences; there is no significance classifier beyond completed/failed tasks and explicit `ghost-remember`. |
| 6 | Unsupported memories do not become verified facts | Implemented and tested | `TestProvenanceNormalisationKeepsMissingEvidenceUnverifiedAndConfidenceBounded`; `TestForeignMemoryStaysUnverifiedAndKeepsReportedConfidenceSeparate`; `TestDecodeRevalidatesVerifiedEnvelopeClaims`; `TestObserveWireRequestAndProvenanceEnvelope` (explicit capped confidence, so Ghostdive's 0→1.0 default never applies) |
| 7 | Contradictions retain their evidence | Implemented and tested | `TestChallengeKeepsBothClaimsAndMarksConflictDisputed`; `TestChallengeWriteKeepsLinkAndEmitsChallengeEvent`; writes always send `skip_auto_supersede=true` and never call transitions |
| 8 | Ghostdive outages do not crash active tasks | Implemented and tested | `TestControllerMemoryTimeoutDoesNotBlockTaskDelivery`; `TestControllerMemoryWriteFailureDoesNotFailTask`; `TestHTTPErrorMappingAndKeyRedaction`; `TestTimeoutConnectionRefusedAndOversizedResponses`; `TestControllerRestartMarksUnfinishedMemoryWriteLostWithoutResending` |
| 9 | Memory behaviour is observable through the TUI | Implemented and tested | `TestMemoryInspectorGolden`; `TestMemoryInspectorFitsSmallTerminalAndShowsOffState`; `TestMemoryInspectorShowsUnavailableReason`; `TestSelectingMemoryShowsFullProvenance`; `TestMemoryCommandsDispatchThroughController`; `TestControllerRestartRestoresMemoryHistoryForInspector` |
| 10 | No model-selected MCP call is required for baseline retrieval or capture | Implemented and tested | Recall and capture run in the runtime (`internal/run/memory.go`); `ghost-recall`/`ghost-remember`/`ghost-challenge` are optional extras. Tests above use no MCP. |

### §6.1 memory test scenarios

| # | Scenario | Status | Test evidence |
| --- | --- | --- | --- |
| 1 | Retrieve before task execution | Implemented and tested | `TestControllerMemoryContextArrivesBeforeHumanTaskSend` |
| 2 | Enforce scope access | Partial | `TestRecallEnforcesAgentTaskAndGlobalScopes`; `TestControllerDoesNotInjectAnotherAgentsPrivateMemory`. Task and per-agent filtering inside a shared space are client-side, not storage-enforced. |
| 3 | Share workspace memory with authorised agents | Implemented and tested | `TestRecallReturnsRelevantWorkspaceMemoryToAnotherAgent`; `TestControllerDelegatedChildReceivesSharedWorkspaceMemory` |
| 4 | Handle empty results | Implemented and tested | `TestRecallEmptyResultsAndDisabledServiceAreSilent` |
| 5 | Handle backend timeout | Implemented and tested | `TestRecallTimeoutReturnsEmptyWithinDeadlineAndReportsFailure`; `TestControllerMemoryTimeoutDoesNotBlockTaskDelivery` |
| 6 | Handle failed consolidation or write | Partial | Write failures, queue overflow, and lost writes are tested by `TestWriteFailureAndQueueOverflowEmitEventsWithoutBlockingCaller`, `TestControllerMemoryWriteFailureDoesNotFailTask`, and `TestControllerRestartMarksUnfinishedMemoryWriteLostWithoutResending`. Ghost has no LLM consolidation. |
| 7 | Keep contradictory memories and link challenges | Implemented and tested | `TestChallengeKeepsBothClaimsAndMarksConflictDisputed`; `TestChallengeWriteKeepsLinkAndEmitsChallengeEvent` |
| 8 | Show missing evidence without upgrading validation | Implemented and tested | `TestProvenanceNormalisationKeepsMissingEvidenceUnverifiedAndConfidenceBounded`; `TestSelectingMemoryShowsFullProvenance` |
| 9 | Treat retrieved instructions as untrusted | Implemented and tested | `TestControllerSanitisesUntrustedMemoryChannelText`; `TestContextSanitisesUntrustedContentAndTruncatesDeterministically` |
| 10 | Enforce the context token budget | Implemented and tested | `TestContextSanitisesUntrustedContentAndTruncatesDeterministically` |

No live Ghostdive call was made. The HTTP adapter was tested with
`httptest`; runtime and TUI tests use fake services and backends. Ghostdive
transitions are not used. Ghost selects spaces and filters task and agent
scopes after retrieval where those scopes share a Ghostdive space. This is not
storage-enforced isolation. The #42 assumptions are documented in
[`design/memory.md`](design/memory.md#ghostdive-42-assumptions).

The tests do not measure whether retrieved memories were relevant or whether
agents used them. The only managed-agent path is Codex; native agents are not
available in this repo.

## Milestone E: Decision agents and CES auto

Step 1 adds validated core decision contracts, a bounded decision service,
fake, Jev, and deterministic rules agents, disabled-by-default `[decisions]`
config, and asynchronous controller event wiring. Fake-agent tests cover
recommendations, failures, limits, timeouts, override records, replay, and restart.
No live Jev transport is configured. Step 2 remains pending: CES auto mode,
safe-boundary selection, fallback application, and operator overrides are not
implemented.

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

Run by the operator on 2026-10-25 (macOS arm64, Go 1.26.8), outside any sandbox, at the tip of milestone D:

| Command | Result |
| --- | --- |
| `gofmt -l .` | Pass; no output |
| `go vet ./...` | Pass |
| `make build test vet` | Pass |
| `go test -race -count=1 ./...` | Pass; every package with tests `ok`, including `internal/serve` |
| `go test -race -count=6 ./internal/{app,run}` and `-count=10 ./internal/run` ×3 | Pass; no flakes (one racy C test assertion fixed in D3) |

**Live Ghostdive: not done.** No call to any real Ghostdive was made; the adapter
is tested against `httptest` servers that follow the routes and DTOs in
Ghostdive's `docs/openapi.yaml` and `internal/httpapi/server.go` (read-only, at
`81ea9ea`).

**Live provider: not done.** No live Codex session was run for milestone C.
At the time, Ghost's config couldn't request a read-only Codex sandbox (`sandbox = "read_only"` was added in E0): `sandboxed = true` maps
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
- Codex sandbox config (milestone E0): an agent can now set `sandbox = "read_only" | "workspace_write" | "full_access"`. When set, it takes precedence over `sandboxed`, and `full_access` with `sandboxed = true` is rejected. This is covered by config, `MakeSessionConfig` and Codex `thread/start` parameter tests. The default is unchanged: an unset or `false` `sandboxed` still maps to `danger-full-access`. TOML can't tell unset from `false` for a bool, and Mark's `ghost.toml` sets `sandboxed = false` explicitly next to `ghost_mode = true`. Changing the default is a behaviour change the plan doesn't sanction. **Open question for Mark:** should an unset sandbox default to `read_only` (or `workspace_write`), with `full_access` required explicitly?
- Memory: no live Ghostdive call has been made. The adapter is verified only against `httptest` servers modelled on Ghostdive's documented routes.
- Memory scopes: Ghostdive's only isolation unit is the space. Task scope, and agent scope inside a shared space, are client-side post-filters in Ghost. Agent privacy is storage-enforced only when `agent_spaces` gives each agent its own space. Any holder of the Ghostdive API key can read the whole space.
- Memory capture keeps completed and failed task results plus explicit `ghost-remember` blocks. There is no significance classifier, and Ghost does no LLM consolidation (Ghostdive consolidates out of band). Whether retrieved memories were relevant or used is not measured.
- Ghost never calls Ghostdive transitions, so nothing Ghost writes becomes `accepted`; governance stays in Ghostdive. The TUI has no correction or deletion controls.
- ghostdive#42 (encryption at rest, draft) assumptions are listed in `design/memory.md`. If #42 changes the wire JSON or provenance shape, only `internal/memory/ghostdive` should need changes.
- The `[memory] trigger` key is kept for config compatibility but is not used by the Ghost Next hooks.
