package run

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
)

// This file is the live CES driver. Ghost owns phase progression: the store
// validates each artifact, the orchestrator chooses the next phase, QAC chooses
// only the resource, and the phase runner executes each phase in a fresh
// session. Nothing here sends task work to the persistent agent sessions, and
// nothing here falls back to dispatchCognition.

// cesPhaseTimeout bounds one cognitive phase. A phase that never returns would
// otherwise strand the work in ACTIVE with no way back to the operator.
const cesPhaseTimeout = 30 * time.Minute

// cesMaxPhaseRepeats bounds how many times one phase may run back to back. A
// contradiction that routes to the phase that produced it would otherwise spin.
const cesMaxPhaseRepeats = 3

// cesPhaseResultMsg transfers a worker result to the run owner.
type cesPhaseResultMsg struct {
	phase    epistemic.Phase
	resource string
	run      cesRun
	result   cognition.PhaseResult
	err      error
}

// cesActive reports whether CES controls an active work item. A terminal store
// remains attached for inspection, but it no longer owns steering or phase
// execution, so the next submission may open a fresh work item.
func (m *owner) cesActive() bool {
	return m.cesStore != nil && m.cesStore.State().Task.Status == epistemic.WorkActive && m.traceError == nil
}

// startCESWork opens a CES store for a new task and runs its first phase. The
// store is created first: if CES cannot be initialized there is no CES work,
// rather than work that quietly runs without it.
func (m *owner) startCESWork(goal string) {
	if m.traceError != nil {
		return
	}
	store, err := epistemic.NewStore(goal)
	if err != nil {
		m.appendEvent(event.Event{Source: "CES", Kind: event.KindError, Message: "start work: " + err.Error()})
		return
	}
	plan, err := m.cognition.StartWork(goal)
	if err != nil {
		m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
		return
	}
	if err := m.cognition.Commit(plan, timeNow()); err != nil {
		m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
		return
	}
	m.cesStore = store
	m.orchestrator = cognition.NewOrchestrator(store)
	m.cesResource = plan.To
	ownerName, ok := m.cognition.Agent(plan.To)
	if !ok {
		m.cesTerminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", fmt.Sprintf("unknown cognitive resource %q", plan.To))
		return
	}
	if _, err := m.createProjectedTask(goal, ownerName, nil, core.CESEnforce); err != nil {
		m.cesTerminal(epistemic.WorkIncomplete, "task_projection_failed", err.Error())
		return
	}
	m.cesPhase = store.State().Task.Phase
	m.cesRepeats = 1
	m.cesPublished = 0
	m.publishCES()
	m.runPhase(plan)
}

// runPhase executes the store's current phase on the resource QAC selected.
// The owner freezes the projection and store before starting the worker.
func (m *owner) runPhase(plan cognition.Plan) {
	if m.cesStore == nil || m.orchestrator == nil || m.traceError != nil {
		return
	}
	task := m.cesStore.State().Task
	if task.Status != epistemic.WorkActive {
		return
	}
	resource := plan.To
	phase := task.Phase
	if agent, ok := m.cognition.Agent(resource); ok {
		m.assignProjectedTask(agent)
	}
	run := m.newCESRun(task.Phase, resource)
	m.cesRunID = run.id
	m.setCESResourceStatus(epistemic.WorkActive, fmt.Sprintf("%s phase starting", strings.ToLower(string(task.Phase))))
	m.traceQACSelection(run, plan)
	if !plan.Initial {
		m.traceCES(run, cognition.PhaseNextStarted, strings.ToUpper(string(task.Phase)), "", "", map[string]string{"resource": resource})
	}
	if m.phaseRuntime == nil {
		m.traceCES(run, "phase_dispatch_failed", "no phase runtime", "", "", nil)
		m.cesTerminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "no phase runtime is configured")
		return
	}
	projection, err := m.cesStore.Project(epistemic.ProjectionRequest{Phase: task.Phase})
	if err != nil {
		m.traceCES(run, "phase_dispatch_failed", "projection", "", "", map[string]string{"error": err.Error()})
		m.cesTerminal(epistemic.WorkIncomplete, "phase_execution_failed", "projection: "+err.Error())
		return
	}
	if _, ok := m.cognition.Agent(resource); !ok {
		m.traceCES(run, "phase_dispatch_failed", "unknown resource", "", "", nil)
		m.cesTerminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", fmt.Sprintf("unknown cognitive resource %q", resource))
		return
	}
	m.cesResource = resource
	request := cognition.PhaseRequest{
		WorkID:     string(task.ID),
		Goal:       task.Goal,
		Phase:      task.Phase,
		ResourceID: resource,
		Projection: projection,
		RunID:      run.id,
	}
	m.traceCES(run, cognition.PhaseScheduled, "resource="+strings.ToUpper(resource), "", "", map[string]string{
		"repeat":             strconv.Itoa(m.cesRepeats),
		"reopen_count":       strconv.Itoa(task.ReopenCount),
		"projection_objects": strconv.Itoa(len(projection.IDs)),
		"timeout_ms":         strconv.FormatInt(cesPhaseTimeout.Milliseconds(), 10),
		"stall_after_ms":     strconv.FormatInt(m.config.Trace.StallAfter().Milliseconds(), 10),
	})
	m.record("", event.Event{Source: "CES", Kind: event.KindStatus, Message: fmt.Sprintf("%s phase running on %s", strings.ToUpper(string(task.Phase)), strings.ToUpper(resource))}, map[string]string{"phase": string(task.Phase), "resource": resource, "phase_run_id": run.id})
	m.transitionProjectedTask(core.TaskRunning, "")
	m.publishCES()
	if m.traceError != nil {
		return
	}
	// The worker validates against a frozen store and configuration.
	frozen, err := epistemic.Replay(m.cesStore.Events())
	if err != nil {
		m.cesTerminal(epistemic.WorkIncomplete, "phase_execution_failed", err.Error())
		return
	}
	request.ValidateArtifact = artifactValidator(frozen, phase, resource, projection)
	cfg, cfgErr := m.phaseSessionConfig(context.Background(), resource, phase)
	runner := cognition.NewPhaseRunner(m.phaseRuntime, func(context.Context, string, epistemic.Phase) (runtime.SessionConfig, error) { return cfg, cfgErr }).WithStallTimeout(m.config.Trace.StallAfter())
	ctx, cancel := context.WithTimeout(m.ctx, cesPhaseTimeout)
	m.cancel = cancel
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer cancel()
		runner = runner.WithObserver(func(e cognition.PhaseEvent) {
			e = clonePhaseEvent(e)
			_ = m.controller.call(func(o *owner) { o.observePhase(e) })
		})
		result, err := runner.Run(ctx, request)
		_ = m.controller.call(func(o *owner) {
			if o.stopping || run.id != o.cesRunID {
				return
			}
			err = errors.Join(err, o.traceError)
			o.handlePhaseResult(cesPhaseResultMsg{phase: phase, resource: resource, run: run, result: result, err: err})
			o.publishBoundary()
		})
	}()

}

// phaseSessionConfig resolves a QAC resource to the runtime configuration of a
// fresh session. The phase runner appends the phase contract to it.
func (m *owner) phaseSessionConfig(_ context.Context, resource string, _ epistemic.Phase) (runtime.SessionConfig, error) {
	if m.cognition == nil {
		return runtime.SessionConfig{}, fmt.Errorf("cognition is not configured")
	}
	agent, ok := m.cognition.Agent(resource)
	if !ok {
		return runtime.SessionConfig{}, fmt.Errorf("unknown cognitive resource %q", resource)
	}
	if m.config.Agents == nil {
		return runtime.SessionConfig{}, fmt.Errorf("no agents are configured")
	}
	backend, ok := (*m.config.Agents)[agent]
	if !ok {
		return runtime.SessionConfig{}, fmt.Errorf("resource %q has no agent configuration", resource)
	}
	return MakeSessionConfig(m.config.Global.InitialPrompt, agent, backend)
}

// handlePhaseResult commits one phase artifact, lets the orchestrator choose
// the next phase, and lets QAC choose the resource that runs it. Every failure
// on this path is terminal for the work; none of them returns to the old path.
func (m *owner) handlePhaseResult(msg cesPhaseResultMsg) {
	if m.cesStore == nil || m.orchestrator == nil || !m.cesActive() {
		return
	}
	if msg.run.workID == "" || msg.run.workID != string(m.cesStore.State().Task.ID) || msg.run.id != m.cesRunID {
		return
	}
	controller := cesController{store: m.cesStore, orchestrator: m.orchestrator, coordinator: m.cognition, taskID: m.activeTaskID, sessions: m.sessions, phase: &m.cesPhase, repeats: &m.cesRepeats, progress: m.progressVerifier, reasoningEffort: m.phaseReasoningEffort(m.cesResource), publish: m.publishCES}
	outcome := controller.process(context.Background(), msg, m)
	if m.traceError != nil {
		return
	}
	if outcome.wait != nil {
		m.beginCESWait(outcome.wait)
		return
	}
	if outcome.terminal != nil {
		task := m.cesStore.State().Task
		task.Status, task.TerminalReason = outcome.terminal.status, outcome.terminal.reason
		if outcome.terminal.detail != "" {
			m.record("", event.Event{Source: "CES", Kind: event.KindError, Message: outcome.terminal.detail}, map[string]string{"reason": task.TerminalReason})
		}
		m.finishCESWork(task)
		return
	}
	if outcome.plan != nil {
		m.runPhase(*outcome.plan)
	}
}

func (m *owner) phaseReasoningEffort(resource string) string {
	if m.cognition == nil || m.config.Agents == nil {
		return ""
	}
	agent, ok := m.cognition.Agent(resource)
	if !ok {
		return ""
	}
	return (*m.config.Agents)[agent].Effort
}

// cesQACRequestFields records the advisory resource request an artifact made
// for the phase CES already chose.
func cesQACRequestFields(request cognition.QACRequest, next epistemic.Phase) map[string]string {
	return map[string]string{
		"requested_phase": string(next),
		"direction":       request.Direction,
		"uncertainty":     strconv.FormatFloat(request.Uncertainty, 'f', 2, 64),
		"novelty":         strconv.FormatFloat(request.Novelty, 'f', 2, 64),
		"expected_gain":   strconv.FormatFloat(request.ExpectedGain, 'f', 2, 64),
		"failed_attempts": strconv.Itoa(request.FailedAttempts),
		"reason":          request.Reason,
	}
}

// cesTerminal ends CES work with an explicit reason and publishes it. CES work
// never ends silently and never degrades into the old execution path.
func (m *owner) cesTerminal(status epistemic.WorkStatus, reason, detail string) {
	if m.cesStore == nil || m.orchestrator == nil {
		return
	}
	controller := cesController{store: m.cesStore}
	terminal := controller.terminal(status, reason, detail)
	task := m.cesStore.State().Task
	task.Status, task.TerminalReason = terminal.status, terminal.reason
	detail = terminal.detail
	if detail != "" {
		m.record("", event.Event{Source: "CES", Kind: event.KindError, Message: detail}, map[string]string{"reason": task.TerminalReason})
	}
	m.finishCESWork(task)
}

// finishCESWork mirrors a terminal CES status into the QAC resource view and
// the operator surfaces. CES owns the status; QAC only learns it.
func (m *owner) finishCESWork(task epistemic.Task) {
	m.clearCESWait()
	m.setCESResourceStatus(task.Status, "CES "+strings.ToLower(string(task.Status)))
	m.cesRunID = ""
	if m.cognition != nil {
		m.cognition.SyncWorkStatus(task.Status, task.TerminalReason, timeNow())
	}
	m.publishCES()
	status := core.TaskFailed
	if task.Status == epistemic.WorkComplete {
		status = core.TaskCompleted
	} else if task.TerminalReason == "operator_canceled" {
		status = core.TaskCancelled
	}
	m.transitionProjectedTask(status, task.TerminalReason)
}

// cesDeltaEmpty reports a delta that would record nothing. The store rejects
// an empty commit, which is the right rule for the store and the wrong outcome
// for an artifact whose only content is its narrative fields.
func cesDeltaEmpty(delta epistemic.Delta) bool {
	count := len(delta.Observations) + len(delta.Claims) + len(delta.Hypotheses) + len(delta.Unknowns) +
		len(delta.Constraints) + len(delta.Frames) + len(delta.Actions) + len(delta.Outcomes) +
		len(delta.Relations) + len(delta.Retractions) + len(delta.StatusChanges)
	return count == 0 && delta.LeadingHypothesis == "" && delta.ActiveFrame == ""
}
