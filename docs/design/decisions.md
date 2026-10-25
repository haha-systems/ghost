# Decision records

Status: Step 1 is implemented. CES auto and automatic decision use are not
started.

## A1. Keep runtime contracts in a leaf package

Status: Implemented for the Phase A contracts; runtime mapping is designed.

Decision: `internal/core` imports only the standard library. Existing packages
map into it at explicit boundaries. This avoids changing current behavior or
creating import cycles. The contracts are implemented; the mapping is not.

## A2. Keep task, CES, and resource authority separate

Status: Existing CES/QAC separation is implemented; generic task wiring is designed.

Decision: a runtime task owns task lifecycle; the CES orchestrator owns phase
choice and completion; QAC owns resource choice; adapters execute provider
work. `Orchestrator.Advance` and `Coordinator.AllocateWithSituation` already
preserve the latter separation. Generic task wiring is designed only. See
[CES](../CES.md) and the [runtime design](runtime.md).

## A3. Keep Jev advisory

Status: Implemented in `internal/cognition`.

Decision: Jev judges cognitive adequacy and may adjust bounded allocation
signals. It cannot select an agent, provider, model, or effort. Deterministic
QAC remains the resource authority. This rule is implemented by
`ParseJevJudgment`, `shouldAssessJev`, and `applyJevAdvice` in
[`internal/cognition/jev.go`](../../internal/cognition/jev.go), and
`Coordinator.AllocateWithSituation` in
[`internal/cognition/coordinator.go`](../../internal/cognition/coordinator.go).

## A4. Make event order explicit

Status: Event contract implemented; runtime publication is not started.

Decision: event IDs identify records; stream sequence establishes order;
timestamps do not. Correlation and causation links explain relationships.
`core.Event` and `core.Sequencer` implement this contract. Runtime-wide
publication and recovery remain unstarted.

## A5. Keep memory separate from CES evidence

Status: Designed. Implementation is not started.

Decision: memory may supply sourced context, while CES continues to own the
current evidence and work state. The integration is designed only; see
[memory](memory.md) and [CES](../CES.md).

## A6. Record decisions as events

Status: Partial. Step 1 adds generic decision request, recommendation,
unavailable, and budget events, plus separate applied, overridden, and ignored
records. The optional owner helper is tested with a fake runtime and agent.
CES does not call the helper automatically. Existing QAC plans still use
`decision.qac`; QAC request and execution records are not separated.

Decision: store the validated compact request and full response, including
optional raw `model_output`, as versioned core events. Link every event to the
request ID. The response is advisory. Runtime code must record its later
application, override, or rejection separately. No explanation field is
required.

## E audit

These findings were checked against the current code before this milestone:

- `internal/cognition/jev.go` defines `JevClient.Assess`, strict
  `ParseJevJudgment`, a trigger limited to repeated execute attempts with
  positive structured evidence of low progress, and advisory score
  adjustments. Unknown progress does not trigger assessment. The Jev path
  cannot choose a resource. `SetJev` and
  `SetJevTimeout` exist, but production code does not call them, and this repo
  has no Jev transport implementation. Step 1 adds an adapter, not a live
  transport.
- QAC uses `github.com/haha-systems/qac`, with its policy, resources, and
  budgets configured under `[qac]`. The cognition coordinator allocates by CES
  phase; `internal/run/roster_qac.go` dispatches the plan. The current adapter
  records a plan as `decision.qac`, including Jev assessment data when present.
  Recommendation and execution remain one QAC path.
- Run-level CES policy is `on` or `off`. A requested policy change waits for
  the next task start while provider work is active. Task-level policy is
  `off`, `observe`, or `enforce`; delegated children inherit it but use the
  standard session path.
- Before step 1, Ghost had no generic decision service, no decision budgets,
  and no automatic CES mode. Step 1 adds those service contracts and budgets.
  It does not add CES auto mode or automatic consultation.

The decision service is separate from run, communication, memory, app, and
Codex packages. It provides per-kind registration and enablement, a call
timeout, run and task limits, and a consecutive-failure breaker with `Reset`.
The rules agent uses a documented word-count/list/sequence heuristic for
`ces_mode`. It is deterministic but is not a model judgment. The Jev adapter
uses the existing strict parser; invalid output becomes an invalid outcome.

The controller can issue requests without waiting in its owner loop. The
request and result return as owner messages and enter the core event log.
Decision events replay through the existing generic core-event projection.
The event helper records request, result, and execution records by request ID.
No production CES path calls it yet. Step 2 owns CES auto policy and its safe
start boundary.
