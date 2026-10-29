package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/qac"
)

// cesController applies CES decisions within the run owner loop. The owner
// records traces and publishes events; there is no second hook adapter.
type cesController struct {
	store           *epistemic.Store
	orchestrator    *cognition.Orchestrator
	coordinator     *cognition.Coordinator
	taskID          core.TaskID
	pinnedResource  string
	sessions        map[string]runtime.Session
	phase           *epistemic.Phase
	repeats         *int
	progress        cognition.ProgressVerifier
	reasoningEffort string
	publish         func()
}

type cesControllerOutcome struct {
	plan     *cognition.Plan
	terminal *cesTerminalDecision
	// wait holds a plan whose resource must be made ready before commit.
	wait *cesWait
}

type cesTerminalDecision struct {
	status epistemic.WorkStatus
	reason string
	detail string
}

func commitRequestForArtifact(store *epistemic.Store, phase epistemic.Phase, resource string, artifact cognition.PhaseArtifact, projection epistemic.Projection, producer epistemic.Producer) (epistemic.CommitRequest, error) {
	delta, err := artifact.Delta()
	if err != nil {
		return epistemic.CommitRequest{}, err
	}
	if phase == epistemic.PhaseClose && completionGate(store.State(), artifact, delta) {
		leading := store.State().Task.LeadingHypothesis
		delta.StatusChanges = append(delta.StatusChanges, epistemic.StatusChange{Kind: epistemic.ObjectHypothesis, Ref: string(leading), Status: string(epistemic.HypothesisConfirmed)})
	}
	return epistemic.CommitRequest{Phase: phase, Producer: producer, Delta: delta, Projection: projection}, nil
}

func completionGate(state epistemic.State, artifact cognition.PhaseArtifact, delta epistemic.Delta) bool {
	if !artifact.CompletionRecommended() || artifact.ClosureResidualUncertainty() != "" || len(delta.Observations) == 0 {
		return false
	}
	if state.Task.LeadingHypothesis == "" || state.Task.ActiveFrame == "" {
		return false
	}
	for _, frame := range state.Frames {
		if frame.ID != state.Task.ActiveFrame || frame.Status != epistemic.FrameActive {
			continue
		}
		for _, condition := range frame.CompletionConditions {
			matched := false
			for _, observation := range delta.Observations {
				if strings.Contains(strings.ToLower(observation.Content), strings.ToLower(condition)) {
					matched = true
					break
				}
			}
			for _, outcome := range delta.Outcomes {
				if strings.Contains(strings.ToLower(outcome.Description), strings.ToLower(condition)) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	}
	return false
}

func (c *cesController) terminal(status epistemic.WorkStatus, reason, detail string) *cesTerminalDecision {
	if c.store != nil && c.store.State().Task.Status == epistemic.WorkActive {
		if _, err := c.store.SetTerminal(status, reason); err != nil {
			slog.Error("set terminal work status", "error", err)
			status, reason, detail = epistemic.WorkIncomplete, "phase_execution_failed", "set terminal work status: "+err.Error()
			// Keep the failed run terminal even when the requested transition failed.
			if fallbackErr := c.store.FailRun(); fallbackErr != nil {
				slog.Error("set failed work status", "error", fallbackErr)
				detail += "; " + fallbackErr.Error()
			}
		}
	}
	return &cesTerminalDecision{status: status, reason: reason, detail: detail}
}

func (c *cesController) eventMeta(fields map[string]string) map[string]string {
	meta := make(map[string]string, len(fields)+1)
	for key, value := range fields {
		meta[key] = value
	}
	if c.taskID != "" {
		meta["task_id"] = string(c.taskID)
	}
	return meta
}

func (c *cesController) process(ctx context.Context, msg cesPhaseResultMsg, o *owner) cesControllerOutcome {
	if c.store == nil || c.orchestrator == nil {
		return cesControllerOutcome{}
	}
	run, sessionID, turnID := msg.run, msg.result.SessionID, msg.result.TurnID
	if msg.err != nil {
		o.record("", event.Event{Source: "CES", Kind: event.KindError, Message: fmt.Sprintf("%s phase failed: %s", strings.ToUpper(string(msg.phase)), msg.err)}, c.eventMeta(map[string]string{"phase": string(msg.phase), "phase_run_id": run.id}))
		reason, detail := "phase_execution_failed", ""
		var exhausted *cognition.ArtifactRepairExhaustedError
		if errors.As(msg.err, &exhausted) {
			detail = msg.err.Error()
			// Exhaustion is always a bounded artifact-protocol failure. The
			// origin flag remains useful for diagnostics, but parse, schema,
			// relationship, and commit-contract exhaustion all deserve the
			// same explicit terminal classification.
			reason = "artifact_repair_exhausted"
		}
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, reason, detail)}
	}
	commitReq, err := commitRequestForArtifact(c.store, msg.phase, msg.resource, msg.result.Artifact, epistemic.Projection{}, epistemic.Producer{})
	if err != nil {
		o.traceCES(run, cognition.PhaseDeltaFailed, "", sessionID, turnID, map[string]string{"error": err.Error()})
		o.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "artifact: " + err.Error()}, c.eventMeta(map[string]string{"phase_run_id": run.id}))
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	delta := commitReq.Delta
	o.traceCES(run, cognition.PhaseDeltaGenerated, "", sessionID, turnID, cesDeltaCounts(delta))
	projection, err := c.store.Project(epistemic.ProjectionRequest{Phase: msg.phase})
	if err != nil {
		o.traceCES(run, cognition.PhaseCommitFailed, "projection", sessionID, turnID, map[string]string{"error": err.Error(), "during": "projection"})
		o.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "projection: " + err.Error()}, c.eventMeta(map[string]string{"phase_run_id": run.id}))
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	producer := epistemic.Producer{Phase: msg.phase, Process: msg.resource, Artifact: string(msg.phase) + "_artifact", Runtime: msg.result.Runtime, Model: msg.result.Model}
	commitReq.Producer = producer
	commitReq.Projection = projection
	if cesDeltaEmpty(delta) {
		o.traceCES(run, cognition.PhaseCommitSkipped, "empty delta", sessionID, turnID, nil)
		o.record("", event.Event{Source: "CES", Kind: event.KindCES, Message: fmt.Sprintf("%s artifact recorded no epistemic change", strings.ToUpper(string(msg.phase)))}, c.eventMeta(map[string]string{"phase": string(msg.phase), "phase_run_id": run.id}))
	} else if committed, err := c.store.Commit(commitReq); err != nil {
		slog.Error("commit CES store", "error", err)
		o.traceCES(run, cognition.PhaseCommitFailed, "", sessionID, turnID, map[string]string{"error": err.Error(), "during": "commit"})
		o.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "commit: " + err.Error()}, c.eventMeta(map[string]string{"phase_run_id": run.id}))
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	} else {
		revision := cesRevisionOf(committed)
		o.traceCES(run, cognition.PhaseCommitSucceeded, "revision="+revision, sessionID, turnID, map[string]string{"revision_id": revision, "store_events": strconv.Itoa(len(committed.Events))})
	}
	o.record("", event.Event{Source: "CES", Kind: event.KindCES, Message: fmt.Sprintf("%s artifact accepted from %s", strings.ToUpper(string(msg.phase)), strings.ToUpper(msg.resource)), SessionID: sessionID, TurnID: turnID}, c.eventMeta(map[string]string{"phase": string(msg.phase), "resource": msg.resource, "phase_run_id": run.id}))
	next, err := c.orchestrator.Advance()
	if err != nil {
		slog.Error("advance CES store", "error", err)
		o.traceCES(run, cognition.PhaseAdvanceFailed, "", sessionID, turnID, map[string]string{"error": err.Error()})
		o.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "advance: " + err.Error()}, c.eventMeta(map[string]string{"phase_run_id": run.id}))
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	o.traceTransition(run, msg.phase, c.store)
	if c.publish != nil {
		c.publish()
	}
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
	workID := msg.run.workID
	if workID == "" {
		workID = string(c.store.State().Task.ID)
	}
	nextRun := o.newCESRunForWork(workID, next, "")
	if c.taskID != "" {
		nextRun.taskID = string(c.taskID)
	}
	if c.pinnedResource != "" {
		nextRun.resource = c.pinnedResource
		o.traceCES(nextRun, "child_resource_pinned", "delegation target", "", "", nil)
		return cesControllerOutcome{plan: &cognition.Plan{To: c.pinnedResource}}
	}
	qacRequest := msg.result.Artifact.QACRequest()
	o.traceCES(nextRun, cognition.PhaseQACRequest, "direction="+qacRequest.Direction, "", "", cesQACRequestFields(qacRequest, next))
	situation := c.cognitiveSituation(msg.phase, qacRequest, msg.result.Artifact)
	if c.coordinator.WillAssessJev(situation) {
		o.traceCES(nextRun, cognition.PhaseJevAssessmentRequested, "compact cognitive state", "", "", cesJevInputFields(situation))
	}
	plan, err := c.coordinator.AllocateWithSituation(ctx, qacRequest, situation, c.sessions, timeNow())
	if err != nil {
		o.traceCES(nextRun, cognition.PhaseQACAllocFailed, "allocate", "", "", map[string]string{"error": err.Error(), "requested_phase": string(next)})
		o.record("", event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()}, nil)
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "")}
	}
	if plan.Jev != nil {
		fields := cesJevAssessmentFields(plan.Jev)
		name := cognition.PhaseJevAssessmentResult
		message := "advisory judgment"
		if plan.Jev.IgnoreReason != "" {
			name, message = cognition.PhaseJevAssessmentIgnored, plan.Jev.IgnoreReason
		}
		o.traceCES(nextRun, name, message, "", "", fields)
	}
	if plan.Action == qac.ActionStop {
		nextRun.resource = plan.To
		o.traceQACSelection(nextRun, plan)
		o.traceCES(nextRun, cognition.PhaseQACAllocFailed, "qac stop", "", "", map[string]string{"requested_phase": string(next), "qac_reason": plan.Decision.Reason})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "qac withdrew the cognitive resource")}
	}
	if c.coordinator.ReadiesUnavailable() {
		if agent, ok := c.coordinator.Agent(plan.To); ok && sessionState(c.sessions[agent]) != runtime.StateIdle {
			return cesControllerOutcome{wait: &cesWait{plan: plan, run: nextRun, agent: agent}}
		}
	}
	return c.commitPlan(plan, nextRun, o)
}

// commitPlan commits a QAC plan whose resource is ready and returns it to run.
func (c *cesController) commitPlan(plan cognition.Plan, nextRun cesRun, o *owner) cesControllerOutcome {
	next := nextRun.phase
	if err := c.coordinator.Commit(plan, timeNow()); err != nil {
		nextRun.resource = plan.To
		o.traceCES(nextRun, cognition.PhaseQACAllocFailed, "commit", "", "", map[string]string{"error": err.Error(), "requested_phase": string(next)})
		o.record("", event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()}, nil)
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "")}
	}
	o.projectQACDecision(plan)
	o.record("", event.Event{Source: "QAC", Kind: event.KindQAC, Message: fmt.Sprintf("%s %s → %s  %.2f / %.2f", strings.ToUpper(string(plan.Action)), strings.ToUpper(string(plan.Decision.From)), strings.ToUpper(string(plan.Decision.To)), plan.Decision.Score, plan.Decision.Threshold)}, o.qacMeta(plan))
	return cesControllerOutcome{plan: &plan}
}

func (c *cesController) cognitiveSituation(phase epistemic.Phase, request cognition.QACRequest, artifact cognition.PhaseArtifact) cognition.CognitiveSituation {
	state := c.store.State()
	currentResource := ""
	if c.coordinator != nil {
		if work := c.coordinator.Work(); work != nil {
			currentResource = work.OwnerResource
		}
	}
	openUnknowns, contradictions := 0, 0
	for _, unknown := range state.Unknowns {
		if unknown.Status == epistemic.UnknownOpen || unknown.Status == epistemic.UnknownReopened {
			openUnknowns++
		}
	}
	for _, relation := range state.Relations {
		if relation.Status == epistemic.RelationActive && relation.Kind == epistemic.RelationContradicts {
			contradictions++
		}
	}
	return cognition.CognitiveSituation{
		Phase: phase, CurrentResource: currentResource, ReasoningEffort: c.reasoningEffort, RepeatedAttempts: *c.repeats,
		FailedAttempts: request.FailedAttempts, ReopenCount: state.Task.ReopenCount,
		ArtifactValid: true, ArtifactAccepted: true,
		Progress: cognition.VerifiedProgress(c.progress, state, artifact), ReportedProgress: artifact.ProgressReport(),
		OpenUnknownCount: openUnknowns, ActiveContradictions: contradictions,
		HasLeadingHypothesis: state.Task.LeadingHypothesis != "", HasActiveFrame: state.Task.ActiveFrame != "",
	}
}
