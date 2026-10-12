package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	store           *epistemic.Store
	orchestrator    *cognition.Orchestrator
	coordinator     *cognition.Coordinator
	sessions        map[string]runtime.Session
	phase           *epistemic.Phase
	repeats         *int
	progress        cognition.ProgressVerifier
	reasoningEffort string
}

type cesControllerHooks interface {
	newRun(epistemic.Phase, string) cesRun
	trace(cesRun, string, string, string, string, map[string]string)
	traceQACSelection(cesRun, cognition.Plan)
	traceTransition(cesRun, epistemic.Phase)
	record(string, event.Event, map[string]string)
	publish()
	qacMeta(cognition.Plan) map[string]string
}

type cesControllerHookFuncs struct {
	newRunFn            func(epistemic.Phase, string) cesRun
	traceFn             func(cesRun, string, string, string, string, map[string]string)
	traceQACSelectionFn func(cesRun, cognition.Plan)
	traceTransitionFn   func(cesRun, epistemic.Phase)
	recordFn            func(string, event.Event, map[string]string)
	publishFn           func()
	qacMetaFn           func(cognition.Plan) map[string]string
}

func (h cesControllerHookFuncs) newRun(phase epistemic.Phase, resource string) cesRun {
	return h.newRunFn(phase, resource)
}
func (h cesControllerHookFuncs) trace(run cesRun, name, detail, sessionID, turnID string, fields map[string]string) {
	h.traceFn(run, name, detail, sessionID, turnID, fields)
}
func (h cesControllerHookFuncs) traceQACSelection(run cesRun, plan cognition.Plan) {
	h.traceQACSelectionFn(run, plan)
}
func (h cesControllerHookFuncs) traceTransition(run cesRun, phase epistemic.Phase) {
	h.traceTransitionFn(run, phase)
}
func (h cesControllerHookFuncs) record(agent string, value event.Event, meta map[string]string) {
	h.recordFn(agent, value, meta)
}
func (h cesControllerHookFuncs) publish() { h.publishFn() }
func (h cesControllerHookFuncs) qacMeta(plan cognition.Plan) map[string]string {
	return h.qacMetaFn(plan)
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

func (c *cesController) process(ctx context.Context, msg cesPhaseResultMsg, hooks cesControllerHooks) cesControllerOutcome {
	if c.store == nil || c.orchestrator == nil || c.coordinator == nil {
		return cesControllerOutcome{}
	}
	run, sessionID, turnID := msg.run, msg.result.SessionID, msg.result.TurnID
	if msg.err != nil {
		hooks.record("", event.Event{Source: "CES", Kind: event.KindError, Message: fmt.Sprintf("%s phase failed: %s", strings.ToUpper(string(msg.phase)), msg.err)}, map[string]string{"phase": string(msg.phase), "phase_run_id": run.id})
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
		hooks.trace(run, cognition.PhaseDeltaFailed, "", sessionID, turnID, map[string]string{"error": err.Error()})
		hooks.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "artifact: " + err.Error()}, map[string]string{"phase_run_id": run.id})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	delta := commitReq.Delta
	hooks.trace(run, cognition.PhaseDeltaGenerated, "", sessionID, turnID, cesDeltaCounts(delta))
	projection, err := c.store.Project(epistemic.ProjectionRequest{Phase: msg.phase})
	if err != nil {
		hooks.trace(run, cognition.PhaseCommitFailed, "projection", sessionID, turnID, map[string]string{"error": err.Error(), "during": "projection"})
		hooks.record("", event.Event{Source: "CES", Kind: event.KindError, Message: "projection: " + err.Error()}, map[string]string{"phase_run_id": run.id})
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "phase_execution_failed", "")}
	}
	producer := epistemic.Producer{Phase: msg.phase, Process: msg.resource, Artifact: string(msg.phase) + "_artifact", Runtime: msg.result.Runtime, Model: msg.result.Model}
	commitReq.Producer = producer
	commitReq.Projection = projection
	if cesDeltaEmpty(delta) {
		hooks.trace(run, cognition.PhaseCommitSkipped, "empty delta", sessionID, turnID, nil)
		hooks.record("", event.Event{Source: "CES", Kind: event.KindCES, Message: fmt.Sprintf("%s artifact recorded no epistemic change", strings.ToUpper(string(msg.phase)))}, map[string]string{"phase": string(msg.phase), "phase_run_id": run.id})
	} else if committed, err := c.store.Commit(commitReq); err != nil {
		slog.Error("commit CES store", "error", err)
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
		slog.Error("advance CES store", "error", err)
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
	qacRequest := msg.result.Artifact.QACRequest()
	hooks.trace(nextRun, cognition.PhaseQACRequest, "direction="+qacRequest.Direction, "", "", cesQACRequestFields(qacRequest, next))
	situation := c.cognitiveSituation(msg.phase, qacRequest, msg.result.Artifact)
	if c.coordinator.WillAssessJev(situation) {
		hooks.trace(nextRun, cognition.PhaseJevAssessmentRequested, "compact cognitive state", "", "", cesJevInputFields(situation))
	}
	plan, err := c.coordinator.AllocateWithSituation(ctx, qacRequest, situation, c.sessions, timeNow())
	if err != nil {
		hooks.trace(nextRun, cognition.PhaseQACAllocFailed, "allocate", "", "", map[string]string{"error": err.Error(), "requested_phase": string(next)})
		hooks.record("", event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()}, nil)
		return cesControllerOutcome{terminal: c.terminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "")}
	}
	if plan.Jev != nil {
		fields := cesJevAssessmentFields(plan.Jev)
		name := cognition.PhaseJevAssessmentResult
		message := "advisory judgment"
		if plan.Jev.IgnoreReason != "" {
			name, message = cognition.PhaseJevAssessmentIgnored, plan.Jev.IgnoreReason
		}
		hooks.trace(nextRun, name, message, "", "", fields)
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
