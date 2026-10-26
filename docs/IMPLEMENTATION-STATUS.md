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

Step 1 adds validated decision contracts, bounded service budgets, fake and Jev
adapters, a deterministic CES rules heuristic, disabled-by-default config, and
event wiring. Step 2 adds `on|off|auto`, fallback, cancellation, recovery, and
manual precedence at safe task boundaries. Step 3 adds the Bloodwire decision
inspector and controls, runtime consultation toggle, and rejection of new
delegations when paused. Auto consultation triggers only when a delegated child
fails; it uses one named router consultation and both decision and router
budgets. Tests use fake runtimes and decision agents. The rules agents are
heuristics, not models. Jev has no production transport.

### Phase 4 acceptance criteria

| # | Acceptance criterion | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Jev participates through a normalized interface | Partial | `TestJevAgentMapsStrictJudgment`; `TestJevAgentRejectsInvalidOutput`; adapter tests use a fake JevClient. No Jev transport exists. |
| 2 | Existing decision behaviour remains supported | Implemented | `TestStartWithManualPolicySkipsCESDecisionAgent`; `TestCESAutoRecommendationOnRunsCESPhases`; `TestCESAutoRecommendationOffRunsStandardPath`; `TestOwnerDecisionRunsOffOwnerAndPersistsRecommendation` |
| 3 | Automatic consultation can be enabled and disabled | Implemented | `TestAutoConsultDisabledDoesNotRequestDecision`; `TestAutoConsultEnabledConsultsOneCandidateOnChildFailure`; `TestAutoConsultCanBeEnabledAtRuntimeWithRulesAgent`; `TestCESAutoCycleAndDecisionSupervisionCommands` |
| 4 | CES automatic mode respects manual overrides | Implemented | `TestOverrideCESDecisionRunsOperatorChoiceAndIgnoresLateRecommendation`; `TestSetCESPolicyOnOverridesPendingAutoDecision`; `TestCESAutoResumesAfterManualOverride` |
| 5 | Decision inputs and outputs are inspectable | Implemented | `TestOwnerDecisionRunsOffOwnerAndPersistsRecommendation`; `TestDecisionInspectorGroupsRequestsAndShowsFullDetails` |
| 6 | Consultation budgets are enforced | Implemented | `TestAutoConsultDecisionBudgetExhaustedSkipsConsultation`; `TestConsultationConcurrencyAndResultShape` |
| 7 | The system works when a decision model is unavailable | Implemented | `TestCESAutoWithoutDecisionServiceUsesConfiguredFallback`; `TestAutoConsultUnavailableAgentDoesNotFailRun`; `TestCESAutoDecisionBudgetExhaustedUsesFallback` |

### §4.6 human supervision

| Operator action | Status | Evidence |
| --- | --- | --- |
| Override a pending CES decision | Implemented | `TestDecisionOverrideCommandAppliesPendingCESChoice`; `TestOverrideCESDecisionRunsOperatorChoiceAndIgnoresLateRecommendation` |
| Stop new agent delegations | Implemented | `TestDelegationCanBeStoppedAndResumed` |
| Disable automatic consultation | Implemented | `TestAutoConsultDisabledDoesNotRequestDecision`; `TestCESAutoCycleAndDecisionSupervisionCommands` |
| Enable or disable CES, including auto | Implemented | `TestCESAutoCycleAndDecisionSupervisionCommands`; `TestCESAutoHeaderShowsDecisionProgressAndAvailability`; `TestCESPolicyOffToOnWaitsForCurrentProviderTurn`; `TestCESPolicyOnToAutoWaitsForCurrentProviderTurn` |
| Inspect decisions and full inputs | Implemented | `TestDecisionInspectorGroupsRequestsAndShowsFullDetails` |
| Resume automatic decisions after an override | Implemented | `TestCESAutoResumesAfterManualOverride` |

### §6.1 CES policy scenarios

| Scenario | Status | Test evidence |
| --- | --- | --- |
| Off to on during provider work | Implemented and tested | `TestCESPolicyOffToOnWaitsForCurrentProviderTurn` |
| On to off during CES work | Implemented and tested | `TestCESPolicyChangeDuringPhaseWaitsForNextStartAndKeepsState` |
| On to auto during provider work | Implemented and tested | `TestCESPolicyOnToAutoWaitsForCurrentProviderTurn` |
| Auto to off during CES work | Implemented and tested | `TestCESPolicyAutoToOffWaitsForCurrentProviderTurn` |
| Delegated child inherits policy and does not request a decision | Implemented and tested | `TestDelegatedChildInheritsPolicyAndDoesNotRequestCESDecision` |
| Human action overrides a pending auto decision | Implemented and tested | `TestOverrideCESDecisionRunsOperatorChoiceAndIgnoresLateRecommendation`; `TestSetCESPolicyOnOverridesPendingAutoDecision` |
| Cancel pending auto start without starting work | Implemented and tested | `TestCancelPendingCESAutoStartRecordsIgnoredAndStartsNothing` |
| Pending decision is interrupted during recovery | Implemented and tested | `TestCESAutoPendingDecisionIsInterruptedOnRecovery` |
| CES on/off, decision fallback, timeout, budget, and unsupported QAC | Implemented and tested | `TestCESAutoRecommendationOnRunsCESPhases`; `TestCESAutoRecommendationOffRunsStandardPath`; `TestCESAutoDecisionFailuresUseOffFallback`; `TestCESAutoDecisionBudgetExhaustedUsesFallback`; `TestCESAutoWithoutDecisionServiceUsesConfiguredFallback`; `TestCESAutoRecommendationOnFallsBackWhenQACBecomesUnavailable`; `TestCESAutoWithoutQACUsesOffAndExplainsWhy` |
| Resume auto mode after an operator override | Implemented and tested | `TestCESAutoResumesAfterManualOverride` |
| TUI cycles CES policy and shows decision controls | Implemented and tested | `TestCESAutoCycleAndDecisionSupervisionCommands`; `TestDecisionOverrideCommandAppliesPendingCESChoice` |


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

## Verification results — milestone E tip

Run by the operator on 2026-10-25 (macOS arm64, Go 1.26.8), outside any sandbox, at the milestone E tip (`ruuzktxp`/`6900fca7`):

| Command | Result |
| --- | --- |
| `gofmt -l .` | clean |
| `go vet ./...` (darwin and `GOOS=linux`) | pass |
| `go test -race -count=1 ./...` | pass, all packages |
| `go test -race -count=10 ./internal/run ./internal/app ./internal/comms ./internal/decision` | pass |
| Race-fix stress: `go test -race -count=300 -cpu 1,2` on the auto-consult and task-status tests | pass (the fix was previously failing about 1–5% of runs) |
| `make build test vet` | pass |
| GitHub CI (ubuntu-latest, Go stable) | pass: https://github.com/haha-systems/ghost/actions/runs/40180669032 |

Two fixes landed with milestone E:
- **CI build on Linux.** `internal/serve` used macOS-only peer-credential calls (`GetsockoptXucred`/`LOCAL_PEERCRED`), so every Linux CI run failed to compile. This came from the base `refactor/headless-serve` change. Now darwin uses `LOCAL_PEERCRED`, Linux uses `SO_PEERCRED`, and other platforms fail closed.
- **Task status race.** This predates milestone E and reproduces at the D tip. A broker snapshot taken before a queued parent transition reached the router could move the projected parent task back to `queued`. Completing the parent then failed with `invalid task transition "queued" -> "completed"` and published duplicate completion events. Router snapshots now never lower a projected task's status (`keepNewerTaskStatus`). Regression tests: `TestFailedDelegatedChildDoesNotRegressParentStatus` and `TestKeepNewerTaskStatusIgnoresStaleRouterSnapshot`.

**Live decision model: not done.** No Jev transport exists. The CES and consultation rules agents are deterministic heuristics. All decision tests use fake runtimes and fake decision agents.

**Live Ghostdive: not done.** No call to any real Ghostdive was made; the adapter
is tested against `httptest` servers that follow the routes and DTOs in
Ghostdive's `docs/openapi.yaml` and `internal/httpapi/server.go` (read-only, at
`81ea9ea`).

**Live provider: one opt-in cycle.** `internal/run/live_review_cycle_test.go` (`//go:build live`, skipped unless `GHOST_LIVE=1`) runs a real WRAITH -> VEIL -> WRAITH review with two read-only Codex sessions (`approval = never`, effort from `GHOST_LIVE_CODEX_EFFORT`, default `low`). It fails on any routing rejection. Run on 2026-10-25 at 22:05 GMT (codex-cli 0.163.0): pass in 14.1 s, with 1 VEIL turn and 2 WRAITH turns. VEIL found the planted `a - b` bug, and WRAITH relayed it to the operator. Two earlier runs that day failed, and the failures led to the commentary-phase and consultation-reply fixes below. One live pass is not evidence that models reliably emit well-formed `ghost-*` blocks.

CES and QAC are separate systems. CES owns phase order, epistemic state, and work status. QAC selects a resource for a CES phase. See [`design/runtime.md`](design/runtime.md), [`design/communication.md`](design/communication.md), and [`design/tui.md`](design/tui.md).

## PR #6 review fixes

Each finding is fixed in its own change, stacked on `feat/ghost-next`, oldest first:

| Finding | Change | Fix | Main tests |
| --- | --- | --- | --- |
| B1, C (regression) | `kqypplwl` | Only `item/completed` (and legacy `item/agentMessage/completed`) assistant messages are agent finals. `item/started` and deltas stay provider events. If the completed text is empty, the delta buffer is used. | `TestCodexAgentMessageLifecycleProjectsOnlyCompletedReply`; `TestNormalizeAgentMessageLifecycle` |
| B3 | `rlkrzwpp` | When a broker enqueue fails, `onFailure` runs, pending-channel bookkeeping is restored, and a visible error is emitted. | broker saturation regression |
| B8 | `kkxtqvxu` | The 16 KiB cap applies to each `ghost-*` block, not to the whole final. | parser prose-size tests |
| B6, B7 | `moxvoout` | Retrieval events persist record IDs, metadata and byte counts, but no text. `memory.context_prepared` is emitted before delivery, and `memory.context_injected` only after the provider accepts the input. | memory event tests |
| B4 | `qrxwskvk` | Broker closures use values snapshotted on the owner goroutine (delegation flag, workspace, memory, ctx). | concurrent delegation-toggle race test |
| B2 | `nowkoqlo` | Routing rejections persist `coordination.routing_rejected` (sender, action, recipient, reason, task) and render in the TUI. Notices are deduplicated by rejection identity. | routing rejection tests |
| B5 | `umrnqotn` | Per-recipient FIFO delivery workers take memory recall and provider delivery off the serial broker (`comms.AsyncDelivery`). A CES policy change is now deferred while a run is active, not only while a provider turn is active. | head-of-line and delivery-order tests |
| A1 | `vnorxysu` | CES auto decides again at every global Start/Send boundary. `Effective` stays `auto`, and the executed on/off decision is recorded on `CESAuto`. | `TestCESAutoDecidesAgainForEachStartBoundary` |
| A3 | `mmxysypz` | CES owner changes go through `Router.ChangeOwner` (`task.owner_changed` is emitted by the router). Broker snapshots keep a newer projected owner until the router catches up, and replay uses the same logic. | owner handoff, stale snapshot and replay tests |
| A2 | `kqqqnvmr` | The controller drives `Router.Tick` on a ticker, with an injectable `Clock`/`TickChannel` and a final tick on `Close`. A consultant's plain final reply resolves its open consultation, and a failed consultant turn fails it. `Consult` re-checks the parent task under the insert lock. | `internal/run/lifecycle_test.go`; comms cancel-vs-consult race test |
| B1 follow-up | `tytulkpp` | A Codex `agentMessage` with `phase = commentary` is a status event, not a reply, and its deltas no longer reach the answer fallback. | `TestCodexCommentaryIsNotProjectedAsReply`; `TestNormalizeAgentMessageCommentaryIsStatus` |
| A2/C follow-up | `ptrqxnxo` | Consultation request frames and the capability brief tell agents to answer consultations and operator tasks in plain prose. `ghost-result` is only for delegated child tasks. | `TestConsultationRequestFrameTellsConsultantHowToReply` |
| B5 follow-up (CI) | `mlytlqyy`, `mnrxklvn` | Queued deliveries for an operator-cancelled task are skipped, and cancellation results still reach the requester. If a delivery lands after the cancel, the session is interrupted. Two tests now wait for async delivery and task creation instead of assuming it is synchronous. | `TestQueuedDeliveryForCancelledTaskIsSkippedButResultsStillFlow`; `TestCancelCommandPrefixAndTaskDetailKey` |
| C (live) | `qmnlwxmx` | Opt-in live WRAITH -> VEIL -> WRAITH cycle (see "Live provider" above). | `TestLiveWraithVeilWraithReviewCycle` |
| B5 follow-up (timing) | `osnwnmnz` | Three tests wait for asynchronously delivered state (start-message delivery, the cancellation result, restart delivery states) instead of reading it immediately. | broker saturation, cancel cascade, restart recovery tests |
| B5 follow-up (race) | `vuxzurkr` | `ghost-result` ownership is checked against the router inside the broker command, not the owner's workspace projection. With async delivery, a child could reply before the owner had synced the task the router just created, so a valid result was rejected and the child stayed queued. Invalid results are still rejected. Also fixes a test-only race in `TestControllerMemoryContextArrivesBeforeHumanTaskSend` (assertions could read the flags before the send callback stored them). | `TestDelegatedChildResultBeforeOwnerSyncIsAccepted` (deterministic, via a broker post-apply test hook); `TestChildResultForTaskItDoesNotOwnIsRejected` |
| CES phase sessions | `fix/phase-session-close` | Phase sessions start with `SessionConfig.Ephemeral`, and the Codex runtime tracks them apart from roster sessions. Starting or closing a TRIAGE/ABDUCE phase session no longer closes the agent's long-lived session (a regression from `bb415d18`). That had left WRAITH `stopped`, so QAC marked it unavailable and stopped the work with `cognitive_resource_unavailable`. | `TestPhaseSessionKeepsTheLongLivedSessionAvailable` (replays the ABDUCE release request); `TestPhaseSessionsStillReplaceEachOther` |

Each change passed `gofmt -l .`, `go vet ./...` (darwin and `GOOS=linux`), `go test -race -count=1 ./...`, `make build test vet`, and `go test -count=5 ./internal/run ./internal/app ./internal/comms`. Behaviour changes to note:
- `Status.Effective` now reports `auto` when auto is requested.
- CES policy is not restored from the event log on restart; it still comes from config.
- `task.owner_changed` has actor `controller` but is now written by the router.

## Known gaps

- Provider turns do not survive process restart. Unfinished tasks fail with `interrupted: ghost restarted`; this milestone does not resume or verify a live provider turn.
- Recovery reads at most 100 files and 20,000 events by default. Older history is omitted and reported in the recovery summary.
- Child CES policy is inherited, but child tasks use the standard session path.
  Automatic CES decisions are not requested for delegated children, and child
  CES execution is not implemented. Auto consultation is a separate,
  failed-child trigger.
- Jev has no production transport. The Jev adapter is tested with a fake
  client. CES and consultation `RulesAgent` behaviours are deterministic
  heuristics, not model judgments.
- `ghost serve` does not expose workspace snapshot or CES-control RPC methods.
- Configured agents should share one workspace root in the TUI. If roots differ, the first agent by name selects the TUI event-log root. `ghost serve` uses the workspace named by each run request.
- The agent channel is a text protocol: fenced `ghost-*` JSON blocks in an agent's final message. The sender is authenticated as the agent's session, but there is no Codex tool/MCP bridge yet. Delivery to agents is framed text through `Send`/`Steer`.
- The broker computes permission intersections, but they are not enforced at the provider tool level. Codex still runs with whatever sandbox the config selects.
- Codex sandbox config: agents can set `sandbox = "read_only" | "workspace_write" | "full_access"`, which takes precedence over the legacy `sandboxed`. **Decision (Mark, 2026-10-25): an unset sandbox defaults to `workspace_write`; `full_access` is explicit opt-in only.** An explicit legacy `sandboxed = false` keeps its old meaning (`danger-full-access`), and `sandboxed = true` stays `workspace-write`. The checked-in `ghost.toml` sets `sandboxed = false`, so its agents still get full access until it is edited. See the mapping table in `design/runtime.md`.
- Memory: no live Ghostdive call has been made. The adapter is verified only against `httptest` servers modelled on Ghostdive's documented routes.
- Memory scopes: Ghostdive's only isolation unit is the space. Task scope, and agent scope inside a shared space, are client-side post-filters in Ghost. Agent privacy is storage-enforced only when `agent_spaces` gives each agent its own space. Any holder of the Ghostdive API key can read the whole space.
- Memory capture keeps completed and failed task results plus explicit `ghost-remember` blocks. There is no significance classifier, and Ghost does no LLM consolidation (Ghostdive consolidates out of band). Whether retrieved memories were relevant or used is not measured.
- Ghost never calls Ghostdive transitions, so nothing Ghost writes becomes `accepted`; governance stays in Ghostdive. The TUI has no correction or deletion controls.
- ghostdive#42 (encryption at rest, draft) assumptions are listed in `design/memory.md`. If #42 changes the wire JSON or provenance shape, only `internal/memory/ghostdive` should need changes.
- Memory delivery whose apply fails is re-queued in `pendingMemory` and retried at every later turn completion. There is no retry cap yet.
- A consultation result is the consultant's whole plain final reply, with `uncertainty` set to "not specified in the consultant's reply". There is no structured consultation-result block.
- When the broker queue is full, a periodic tick is skipped silently, and the next tick retries.
- The `[memory] trigger` key is kept for config compatibility but is not used by the Ghost Next hooks.
