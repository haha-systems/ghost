package run

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
)

type childCESWork struct {
	task         core.Task
	store        *epistemic.Store
	orchestrator *cognition.Orchestrator
	resource     string
	phase        epistemic.Phase
	repeats      int
	runID        string
	ctx          context.Context
	cancel       context.CancelFunc
}

func (o *owner) startChildCES(task core.Task) {
	if o.stopping || task.Parent == nil || task.CESPolicy != core.CESEnforce || o.childCES[task.ID] != nil {
		return
	}
	if o.traceError != nil {
		o.failChildCES(task.ID, "phase_execution_failed: trace write failed")
		return
	}
	if current, ok := o.router.Snapshot().Task(task.ID); !ok || current.Status.IsTerminal() {
		return
	}
	if o.cognition == nil {
		o.failChildCES(task.ID, "ces_unavailable: QAC is not configured")
		return
	}
	if o.phaseRuntime == nil {
		o.failChildCES(task.ID, "ces_unavailable: no phase runtime is configured")
		return
	}
	agent, ok := o.workspace.agentName(task.Owner)
	if !ok || o.config.Agents == nil {
		o.failChildCES(task.ID, "ces_unavailable: child owner has no agent configuration")
		return
	}
	backend, ok := (*o.config.Agents)[agent]
	if !ok {
		o.failChildCES(task.ID, "ces_unavailable: child owner has no agent configuration")
		return
	}
	if _, err := MakeSessionConfig(o.config.Global.InitialPrompt, agent, backend); err != nil {
		o.failChildCES(task.ID, "ces_unavailable: child owner session configuration failed: "+err.Error())
		return
	}
	goal := strings.TrimSpace(task.Objective)
	if task.ExpectedDeliverable != "" {
		goal += "\n\nExpected deliverable: " + strings.TrimSpace(task.ExpectedDeliverable)
	}
	store, err := epistemic.NewStore(goal)
	if err != nil {
		o.failChildCES(task.ID, "phase_execution_failed: "+err.Error())
		return
	}
	ctx, cancel := childCESContext(o.ctx, task)
	work := &childCESWork{
		task: task, store: store, orchestrator: cognition.NewOrchestrator(store),
		resource: agent, phase: store.State().Task.Phase, repeats: 1, ctx: ctx, cancel: cancel,
	}
	o.childCES[task.ID] = work
	o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
		return router.TransitionTask(task.ID, core.TaskRunning, "")
	}})
	o.record("", event.Event{Source: "CES", Kind: event.KindStatus, Message: "delegated child CES started"}, map[string]string{"task_id": string(task.ID), "resource": agent})
	o.runChildCESPhase(work)
	o.publishBoundary()
}

func childCESContext(parent context.Context, task core.Task) (context.Context, context.CancelFunc) {
	var deadline *time.Time
	if task.Limits.Budget.Deadline != nil {
		value := *task.Limits.Budget.Deadline
		deadline = &value
	}
	if task.Limits.Budget.MaxDuration > 0 {
		value := task.CreatedAt.Add(task.Limits.Budget.MaxDuration)
		if deadline == nil || value.Before(*deadline) {
			deadline = &value
		}
	}
	if deadline == nil {
		return context.WithCancel(parent)
	}
	return context.WithDeadline(parent, *deadline)
}

func (o *owner) runChildCESPhase(work *childCESWork) {
	if work == nil || o.stopping || work.ctx.Err() != nil || o.childCES[work.task.ID] != work {
		return
	}
	if task, ok := o.router.Snapshot().Task(work.task.ID); !ok || task.Status.IsTerminal() {
		work.cancel()
		delete(o.childCES, work.task.ID)
		return
	}
	task := work.store.State().Task
	if task.Status != epistemic.WorkActive {
		return
	}
	phase := task.Phase
	run := o.newCESRunForWork(string(work.task.ID), phase, work.resource)
	work.runID = run.id
	o.traceCES(run, "child_resource_pinned", "delegation target", "", "", map[string]string{"task_id": string(work.task.ID)})
	if o.traceError != nil || o.childCES[work.task.ID] != work {
		return
	}
	projection, err := work.store.Project(epistemic.ProjectionRequest{Phase: phase})
	if err != nil {
		o.finishChildCES(work, cesTerminalDecision{status: epistemic.WorkIncomplete, reason: "phase_execution_failed", detail: "projection: " + err.Error()})
		return
	}
	frozen, err := epistemic.Replay(work.store.Events())
	if err != nil {
		o.finishChildCES(work, cesTerminalDecision{status: epistemic.WorkIncomplete, reason: "phase_execution_failed", detail: err.Error()})
		return
	}
	request := cognition.PhaseRequest{
		WorkID: string(work.task.ID), Goal: work.store.State().Task.Goal, Phase: phase,
		ResourceID: work.resource, Projection: projection, RunID: run.id,
		ValidateArtifact: artifactValidator(frozen, phase, work.resource, projection),
	}
	o.traceCES(run, cognition.PhaseScheduled, "resource="+strings.ToUpper(work.resource), "", "", map[string]string{
		"task_id": string(work.task.ID), "repeat": strconv.Itoa(work.repeats),
		"reopen_count": strconv.Itoa(task.ReopenCount), "projection_objects": strconv.Itoa(len(projection.IDs)),
		"timeout_ms":     strconv.FormatInt(cesPhaseTimeout.Milliseconds(), 10),
		"stall_after_ms": strconv.FormatInt(o.config.Trace.StallAfter().Milliseconds(), 10),
	})
	o.record("", event.Event{Source: "CES", Kind: event.KindStatus, Message: fmt.Sprintf("%s phase running on %s", strings.ToUpper(string(phase)), strings.ToUpper(work.resource))}, map[string]string{
		"task_id": string(work.task.ID), "phase": string(phase), "resource": work.resource, "phase_run_id": run.id,
	})
	runner := cognition.NewPhaseRunner(o.phaseRuntime, func(_ context.Context, _ string, _ epistemic.Phase) (runtime.SessionConfig, error) {
		backend, ok := (*o.config.Agents)[work.resource]
		if !ok {
			return runtime.SessionConfig{}, fmt.Errorf("resource %q has no agent configuration", work.resource)
		}
		return MakeSessionConfig(o.config.Global.InitialPrompt, work.resource, backend)
	}).WithStallTimeout(o.config.Trace.StallAfter())
	phaseCtx, phaseCancel := context.WithTimeout(work.ctx, cesPhaseTimeout)
	o.workers.Add(1)
	go func() {
		defer o.workers.Done()
		defer phaseCancel()
		runner = runner.WithObserver(func(e cognition.PhaseEvent) {
			e = clonePhaseEvent(e)
			if e.Fields == nil {
				e.Fields = map[string]string{}
			}
			e.Fields["task_id"] = string(work.task.ID)
			_ = o.controller.call(func(current *owner) { current.observePhase(e) })
		})
		result, err := runner.Run(phaseCtx, request)
		_ = o.controller.call(func(current *owner) {
			current.handleChildCESPhaseResult(work, cesPhaseResultMsg{phase: phase, resource: work.resource, run: run, result: result, err: err})
			current.publishBoundary()
		})
	}()
}

func (o *owner) handleChildCESPhaseResult(work *childCESWork, msg cesPhaseResultMsg) {
	if work == nil || o.stopping || o.childCES[work.task.ID] != work || work.ctx.Err() != nil || msg.run.id != work.runID {
		return
	}
	if task, ok := o.router.Snapshot().Task(work.task.ID); !ok || task.Status.IsTerminal() {
		work.cancel()
		delete(o.childCES, work.task.ID)
		return
	}
	msg.err = errors.Join(msg.err, o.traceError)
	controller := cesController{
		store: work.store, orchestrator: work.orchestrator, taskID: work.task.ID,
		pinnedResource: work.resource, phase: &work.phase, repeats: &work.repeats,
		progress: o.progressVerifier, publish: func() { o.publishChildCES(work) },
	}
	outcome := controller.process(context.Background(), msg, o)
	if o.traceError != nil {
		return
	}
	if outcome.terminal != nil {
		o.finishChildCES(work, *outcome.terminal)
		return
	}
	if outcome.plan != nil {
		o.runChildCESPhase(work)
	}
}

func (o *owner) publishChildCES(work *childCESWork) {
	if work == nil {
		return
	}
	task := work.store.State().Task
	o.record("", event.Event{Source: "CES", Kind: event.KindCES, Message: fmt.Sprintf("delegated child phase advanced to %s", strings.ToUpper(string(task.Phase)))}, map[string]string{
		"task_id": string(work.task.ID), "phase": string(task.Phase), "work_status": string(task.Status),
	})
}

func (o *owner) finishChildCES(work *childCESWork, terminal cesTerminalDecision) {
	if work == nil || o.childCES[work.task.ID] != work {
		return
	}
	task := work.store.State().Task
	task.Status, task.TerminalReason = terminal.status, terminal.reason
	work.cancel()
	delete(o.childCES, work.task.ID)
	if terminal.detail != "" {
		o.record("", event.Event{Source: "CES", Kind: event.KindError, Message: terminal.detail}, map[string]string{"task_id": string(work.task.ID), "reason": terminal.reason})
	}
	if terminal.status == epistemic.WorkComplete {
		result := childCESResult(work)
		o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
			_, err := router.Complete(o.ctx, work.task.ID, result)
			return err
		}})
		return
	}
	detail := terminal.detail
	if detail == "" {
		detail = terminal.reason
	}
	reason := "CES " + strings.ToLower(string(terminal.status)) + ": " + detail
	o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
		_, err := router.Fail(o.ctx, work.task.ID, reason)
		return err
	}})
}

func childCESResult(work *childCESWork) string {
	state := work.store.State()
	for _, hypothesis := range state.Hypotheses {
		if hypothesis.ID == state.Task.LeadingHypothesis && strings.TrimSpace(hypothesis.Mechanism) != "" {
			return "Leading hypothesis: " + strings.TrimSpace(hypothesis.Mechanism)
		}
	}
	if len(state.Outcomes) > 0 {
		return "Closing outcome: " + strings.TrimSpace(state.Outcomes[len(state.Outcomes)-1].Description)
	}
	if len(state.Observations) > 0 {
		return "Closing observation: " + strings.TrimSpace(state.Observations[len(state.Observations)-1].Content)
	}
	return "CES work complete."
}

func (o *owner) failChildCES(id core.TaskID, reason string) {
	o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
		_, err := router.Fail(o.ctx, id, reason)
		return err
	}})
}

func (o *owner) hasChildCESRun(runID string) bool {
	for _, work := range o.childCES {
		if work.runID == runID {
			return true
		}
	}
	return false
}
