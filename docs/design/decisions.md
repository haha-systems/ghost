# Decision records

Status: Designed. The existing QAC and Jev authority split is implemented; the
new decision event records are not started.

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

Status: Designed. Implementation is not started.

Decision: QAC/Jev request, accepted advice, ignored advice, and final resource
choice should be separate decision events. Store compact inputs and outcomes,
not private prompts or full model output. Existing trace messages record some
QAC/Jev activity, but a versioned decision event stream is not present.
