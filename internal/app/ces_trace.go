package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
	ghosttrace "github.com/haha-systems/ghost/internal/trace"
)

// This file makes CES execution reconstructable from the JSONL trace alone.
// Phase events go to the trace, not to the operator views: the views show
// what matters, the trace shows everything needed to explain a run.
//
// Every CES trace record carries work_id, phase, resource_id, agent_id and
// phase_run_id, plus session_id and turn_id once known. Agent names are never
// used to correlate: a persistent agent and a phase session for the same agent
// may run at once.

// cesRun identifies one phase invocation for trace records written from the
// UI goroutine.
type cesRun struct {
	id       string
	workID   string
	phase    epistemic.Phase
	resource string
	agent    string
}

func (r cesRun) meta(fields map[string]string) map[string]string {
	meta := map[string]string{
		"ces_event":    "",
		"work_id":      r.workID,
		"phase":        string(r.phase),
		"resource_id":  r.resource,
		"agent_id":     r.agent,
		"phase_run_id": r.id,
	}
	for key, value := range fields {
		meta[key] = value
	}
	return meta
}

// newCESRun prepares the correlation for a phase about to run.
func (m *Model) newCESRun(phase epistemic.Phase, resource string) cesRun {
	run := cesRun{id: cognition.NewPhaseRunID(), phase: phase, resource: resource}
	if m.cesStore != nil {
		run.workID = string(m.cesStore.State().Task.ID)
	}
	if m.cognition != nil {
		run.agent, _ = m.cognition.Agent(resource)
	}
	return run
}

// traceCES writes one CES record to the trace only.
func (m *Model) traceCES(run cesRun, name, message, sessionID, turnID string, fields map[string]string) {
	if m.trace == nil {
		return
	}
	meta := run.meta(fields)
	meta["ces_event"] = name
	_ = m.trace.Write(event.Event{
		Time:      timeNow(),
		Source:    "CES",
		Kind:      event.Kind(name),
		Message:   cesTraceMessage(string(run.phase), name, message),
		SessionID: sessionID,
		TurnID:    turnID,
		Metadata:  meta,
	})
}

// traceQACSelection records QAC's resource choice for a phase CES already
// selected. The two decisions are separate records so they are never confused.
func (m *Model) traceQACSelection(run cesRun, plan cognition.Plan) {
	d := plan.Decision
	fields := map[string]string{
		"requested_phase": string(run.phase),
		"qac_plan_id":     plan.ID,
		"qac_action":      string(plan.Action),
		"qac_from":        plan.From,
		"qac_to":          plan.To,
		"qac_score":       strconv.FormatFloat(d.Score, 'f', 4, 64),
		"qac_threshold":   strconv.FormatFloat(d.Threshold, 'f', 4, 64),
		"qac_reason":      d.Reason,
		"qac_initial":     strconv.FormatBool(plan.Initial),
	}
	if len(d.Factors) > 0 {
		fields["qac_factors"] = fmt.Sprintf("%+v", d.Factors)
	}
	if len(d.Eligibility) > 0 {
		fields["qac_eligibility"] = fmt.Sprintf("%+v", d.Eligibility)
	}
	m.traceCES(run, cognition.PhaseResourceSelected, fmt.Sprintf("resource=%s action=%s", strings.ToUpper(plan.To), plan.Action), "", "", fields)
}

// traceTransition records how the orchestrator moved the work after a commit.
func (m *Model) traceTransition(run cesRun, from epistemic.Phase) {
	if m.cesStore == nil {
		return
	}
	task := m.cesStore.State().Task
	fields := map[string]string{
		"from_phase":   string(from),
		"to_phase":     string(task.Phase),
		"work_status":  string(task.Status),
		"reopen_count": strconv.Itoa(task.ReopenCount),
	}
	if last := task.LastTransition; last != nil && last.To == task.Phase && task.Phase != from {
		fields["reason"] = last.Reason
		if last.TargetID != "" {
			fields["contradicted_target_id"] = string(last.TargetID)
			fields["contradicted_target_kind"] = string(last.TargetKind)
		}
	}
	switch {
	case task.Status != epistemic.WorkActive:
		fields["reason"] = "work " + string(task.Status)
		if task.TerminalReason != "" {
			fields["terminal_reason"] = task.TerminalReason
		}
	case task.Phase == from:
		fields["reason"] = "phase rerun"
	}
	message := fmt.Sprintf("%s -> %s", strings.ToUpper(string(from)), strings.ToUpper(string(task.Phase)))
	if task.Status != epistemic.WorkActive {
		message = fmt.Sprintf("%s -> %s", strings.ToUpper(string(from)), strings.ToUpper(string(task.Status)))
	}
	m.traceCES(run, cognition.PhaseTransition, message, "", "", fields)
}

// phaseObserver adapts runner events into trace records and an optional app
// event sink. It runs on the runner goroutine; the sink must only enqueue the
// event for later handling by the Bubble Tea model goroutine.
func phaseObserver(w *ghosttrace.Writer, sinks ...func(cognition.PhaseEvent)) cognition.PhaseObserver {
	if w == nil && len(sinks) == 0 {
		return nil
	}
	return func(e cognition.PhaseEvent) {
		for _, sink := range sinks {
			if sink != nil {
				sink(e)
			}
		}
		meta := map[string]string{
			"ces_event":    e.Name,
			"work_id":      e.WorkID,
			"phase":        e.Phase,
			"resource_id":  e.ResourceID,
			"agent_id":     e.AgentID,
			"phase_run_id": e.RunID,
			"elapsed_ms":   strconv.FormatInt(e.Elapsed.Milliseconds(), 10),
		}
		for key, value := range e.Fields {
			meta[key] = value
		}
		record := event.Event{
			Time:      e.Time,
			Source:    "CES",
			Kind:      event.Kind(e.Name),
			Message:   cesTraceMessage(e.Phase, e.Name, ""),
			SessionID: e.SessionID,
			TurnID:    e.TurnID,
			Metadata:  meta,
		}
		if rt := e.Runtime; rt != nil {
			record.Raw = rt.Raw
			if rt.SessionID != "" {
				record.SessionID = rt.SessionID
			}
			if e.Name == cognition.PhaseActivity {
				// Activity reads like the agent's own log line, while the
				// metadata keeps it bound to this phase run.
				record.Source = strings.ToUpper(e.AgentID)
				record.Kind = activityKind(rt.Kind)
				record.Message = rt.Summary
			} else {
				record.Message = cesTraceMessage(e.Phase, e.Name, rt.Summary)
			}
		}
		if w != nil {
			_ = w.Write(record)
		}
	}
}

func activityKind(kind runtime.EventKind) event.Kind {
	switch kind {
	case runtime.KindMessage:
		return event.KindResponse
	case "":
		return event.KindStatus
	default:
		return event.Kind(kind)
	}
}

// cesTraceMessage renders "triage.session_started detail".
func cesTraceMessage(phase, name, detail string) string {
	label := strings.TrimPrefix(name, "phase_")
	if phase != "" {
		label = strings.ToLower(phase) + "." + label
	}
	if detail == "" {
		return label
	}
	return label + " " + detail
}

func cesDeltaCounts(delta epistemic.Delta) map[string]string {
	return map[string]string{
		"observations":   strconv.Itoa(len(delta.Observations)),
		"claims":         strconv.Itoa(len(delta.Claims)),
		"hypotheses":     strconv.Itoa(len(delta.Hypotheses)),
		"unknowns":       strconv.Itoa(len(delta.Unknowns)),
		"constraints":    strconv.Itoa(len(delta.Constraints)),
		"frames":         strconv.Itoa(len(delta.Frames)),
		"actions":        strconv.Itoa(len(delta.Actions)),
		"outcomes":       strconv.Itoa(len(delta.Outcomes)),
		"relations":      strconv.Itoa(len(delta.Relations)),
		"retractions":    strconv.Itoa(len(delta.Retractions)),
		"status_changes": strconv.Itoa(len(delta.StatusChanges)),
	}
}

func cesRevisionOf(result epistemic.CommitResult) string {
	for _, e := range result.Events {
		if e.RevisionID != "" {
			return string(e.RevisionID)
		}
	}
	return ""
}
