# Decision records

Status: Milestone E Steps 1–3 are implemented. Jev has a tested adapter but no
production transport; rules agents are heuristics, not models.

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

Status: Implemented for CES auto. Step 1 adds generic decision request, recommendation,
unavailable, and budget events, plus separate applied, overridden, and ignored
records. Step 2 connects CES auto to the asynchronous owner helper. Fake-agent
tests check the recommendation and later application as separate records.
Existing QAC plans still use `decision.qac`; QAC request and execution records
are not separated.

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
- Run-level CES policy is `on`, `off`, or `auto`. A requested policy change waits for
  the next task start while provider work is active. Run-level `auto` asks a
  decision agent to choose `ces_on` or `ces_off` at Start. Task-level policy is
  `off`, `observe`, or `enforce`; delegated children inherit it but use the
  standard session path and do not request a decision.
- If QAC is not configured, `auto` resolves to `off` with reason
  `unsupported: QAC not configured`. If the decision service cannot recommend,
  Ghost applies `[decisions].fallback` (default `off`). The operator can cancel
  or override a pending request. A late recommendation after an override is
  recorded and ignored. Changes to the run policy during a provider turn wait
  for the next Start.
- Automatic decision requests include the goal, selected participants, QAC
  state, the entry resource, and the prior run outcome when one is available.
  A missing decision service is recorded as unavailable and uses the same
  fallback. If QAC becomes unavailable while a request is pending, Ghost
  records the recommendation as ignored and applies `ces_off`.
- Automatic consultation is off by default and may be toggled at runtime. It
  only runs after a delegated child task fails. The request contains the failed
  task, parent agent, and eligible roster candidates. The deterministic rules
  agent selects the first candidate other than the parent; the router opens one
  named consultation and applies its own concurrency and timeout limits. The
  decision service also enforces its call, run, and task budgets. An unavailable
  decision agent or exhausted budget does not fail the parent run.
  `:delegation off` records rejections for new agent delegations while existing
  tasks finish.
- The Bloodwire decisions inspector groups decision events by request ID and
  shows the stored request, output, and execution records. The header reports
  decision service availability and CES auto status. Human overrides use the
  controller and remain separate from recommendations.
- An unfinished CES auto request found during recovery is recorded as ignored
  with reason `interrupted`; it is not resumed. Auto decisions and recovery are
  tested with fake agents and runtimes. No live provider or Jev transport is
  claimed.

The decision service is separate from run, communication, memory, app, and
Codex packages. It provides per-kind registration and enablement, a call
timeout, run and task limits, and a consecutive-failure breaker with `Reset`.
The rules agent uses a documented word-count/list/sequence heuristic for
`ces_mode`. It is deterministic but is not a model judgment. The Jev adapter
uses the existing strict parser; invalid output becomes an invalid outcome.

The controller issues requests without waiting in its owner loop. The request
and result return as owner messages and enter the core event log. Decision
events replay through the existing generic core-event projection. CES auto
uses the helper at the Start boundary and records request, result, application,
override, or rejection by request ID. Consultation decisions run only after a
failed delegated child, use one named router consultation, and record applied
or ignored outcomes. The CES and consultation rules agents are deterministic
heuristics; neither is a model judgment. The TUI reads the controller snapshot
and adds no decision state of its own.
