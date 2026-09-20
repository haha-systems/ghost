package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/haha-systems/qac"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
)

// cesController owns deterministic CES coordination. The Bubble Tea model
// supplies side-effect hooks, but the controller alone mutates the epistemic
// store, advances phases, applies repeat limits, and allocates QAC resources.
type cesController struct {
	store        *epistemic.Store
	orchestrator *cognition.Orchestrator
	coordinator  *cognition.Coordinator
	sessions     map[string]runtime.Session
	phase        *epistemic.Phase
	repeats      *int
}

type cesControllerHooks struct {
	newRun            func(epistemic.Phase, string) cesRun
	trace             func(cesRun, string, string, string, string, map[string]string)
	traceQACSelection func(cesRun, cognition.Plan)
	traceTransition   func(cesRun, epistemic.Phase)
	record            func(string, event.Event, map[string]string)
	publish           func()
	qacMeta           func(cognition.Plan) map[string]string
}

type cesControllerOutcome struct {
	plan     *cognition.Plan
	terminal *cesTerminalDecision
}

type cesTerminalDecision struct {
	status epistemic.WorkStatus
	reason string
	detail string
}

func (c *cesController) terminal(status epistemic.WorkStatus, reason, detail string) *cesTerminalDecision {
	if c.store != nil && c.store.State().Task.Status == epistemic.WorkActive {
		_, _ = c.store.SetTerminal(status, reason)
	}
	return &cesTerminalDecision{status: status, reason: reason, detail: detail}
}

func (c *cesController) process(ctx context.Context, msg cesPhaseResultMsg, hooks cesControllerHooks) cesControllerOutcome {
	if c.store == nil || c.orchestrator == nil || c.coordinator == nil {
		return cesControllerOutcome{}
	}
	run, sessionID, turnID := msg.run, msg.result.SessionID, msg.result.TurnID
	if msg.err != nil {
		hooks.record("", event.Event{Source: "CES", Kind: event.KindError, Message: fmt.Sprintf("%s phase failed: %s", strings.ToUpper(string(msg.phase)), msg.err)}, map[string]string{"phase": string(msg.phase), "phase_run_id": run.id})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	delta, err := msg.result.Artifact.Delta()
	if err != nil {
		hooks.trace(run, cognition.PhaseDeltaFailed, "", sessionID, turnID, map[string]string{"error": err.Error()})
		hooks.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "artifact: " + err.Error()}, map[string]string{"phase_run_id": run.id})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	if msg.phase == epistemic.PhaseClose && msg.result.Artifact.CompletionRecommended() {
		if leading := c.store.State().Task.LeadingHypothesis; leading != "" {
			delta.StatusChanges = append(delta.StatusChanges, epistemic.StatusChange{Kind: epistemic.ObjectHypothesis, Ref: string(leading), Status: string(epistemic.HypothesisConfirmed)})
		}
	}
	hooks.trace(run, cognition.PhaseDeltaGenerated, "", sessionID, turnID, cesDeltaCounts(delta))
	projection, err := c.store.Project(epistemic.ProjectionRequest{Phase: msg.phase})
	if err != nil {
		hooks.trace(run, cognition.PhaseCommitFailed, "projection", sessionID, turnID, map[string]string{"error": err.Error(), "during": "projection"})
		hooks.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "projection: " + err.Error()}, map[string]string{"phase_run_id": run.id})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	producer := epistemic.Producer{Phase: msg.phase, Process: msg.resource, Artifact: string(msg.phase) + "_artifact", Runtime: msg.result.Runtime, Model: msg.result.Model}
	if cesDeltaEmpty(delta) {
		hooks.trace(run, cognition.PhaseCommitSkipped, "empty delta", sessionID, turnID, nil)
		hooks.record("", event.Event{Source: "CES", Kind: event.KindCES, Message: fmt.Sprintf("%s artifact recorded no epistemic change", strings.ToUpper(string(msg.phase)))}, map[string]string{"phase": string(msg.phase), "phase_run_id": run.id})
	} else if committed, err := c.store.Commit(epistemic.CommitRequest{Phase: msg.phase, Producer: producer, Delta: delta, Projection: projection}); err != nil {
		hooks.trace(run, cognition.PhaseCommitFailed, "", sessionID, turnID, map[string]string{"error": err.Error(), "during": "commit"})
		hooks.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "commit: " + err.Error()}, map[string]string{"phase_run_id": run.id})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	} else {
		revision := cesRevisionOf(committed)
		hooks.trace(run, cognition.PhaseCommitSucceeded, "revision="+revision, sessionID, turnID, map[string]string{"revision_id": revision, "store_events": strconv.Itoa(len(committed.Events))})
	}
	hooks.record("", event.Event{Source: "CES", Kind: event.KindCES, Message: fmt.Sprintf("%s artifact accepted from %s", strings.ToUpper(string(msg.phase)), strings.ToUpper(msg.resource)), SessionID: sessionID, TurnID: turnID}, map[string]string{"phase": string(msg.phase), "resource": msg.resource, "phase_run_id": run.id})
	next, err := c.orchestrator.Advance()
	if err != nil {
		hooks.trace(run, cognition.PhaseAdvanceFailed, "", sessionID, turnID, map[string]string{"error": err.Error()})
		hooks.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "advance: " + err.Error()}, map[string]string{"phase_run_id": run.id})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	hooks.traceTransition(run, msg.phase)
	hooks.publish()
	if task := c.store.State().Task; task.Status != epistemic.WorkActive {
		return cesControllerOutcome{terminal: &cesTerminalDecision{status: task.Status, reason: task.TerminalReason}}
	}
	if next == *c.phase {
		(*c.repeats)++
	} else {
		*c.phase, *c.repeats = next, 1
	}
	if *c.repeats > cesMaxPhaseRepeats {
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "reopen_budget_exhausted", fmt.Sprintf("%s repeated %d times without progress", strings.ToUpper(string(next)), *c.repeats-1))}
	}
	nextRun := hooks.newRun(next, "")
	request := msg.result.Artifact.QACRequest()
	hooks.trace(nextRun, cognition.PhaseQACRequest, "direction="+request.Direction, "", "", cesQACRequestFields(request, next))
	plan, err := c.coordinator.Allocate(ctx, request, c.sessions, timeNow())
	if err != nil {
		hooks.trace(nextRun, cognition.PhaseQACAllocFailed, "allocate", "", "", map[string]string{"error": err.Error(), "requested_phase": string(next)})
		hooks.record("", event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()}, nil)
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "")}
	}
	if plan.Action == qac.ActionStop {
		nextRun.resource = plan.To
		hooks.traceQACSelection(nextRun, plan)
		hooks.trace(nextRun, cognition.PhaseQACAllocFailed, "qac stop", "", "", map[string]string{"requested_phase": string(next), "qac_reason": plan.Decision.Reason})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "qac withdrew the cognitive resource")}
	}
	if err := c.coordinator.Commit(plan, timeNow()); err != nil {
		nextRun.resource = plan.To
		hooks.trace(nextRun, cognition.PhaseQACAllocFailed, "commit", "", "", map[string]string{"error": err.Error(), "requested_phase": string(next)})
		hooks.record("", event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()}, nil)
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "")}
	}
	hooks.record("", event.Event{Source: "QAC", Kind: event.KindQAC, Message: fmt.Sprintf("%s %s → %s  %.2f / %.2f", strings.ToUpper(string(plan.Action)), strings.ToUpper(string(plan.Decision.From)), strings.ToUpper(string(plan.Decision.To)), plan.Decision.Score, plan.Decision.Threshold)}, hooks.qacMeta(plan))
	return cesControllerOutcome{plan: &plan}
}
