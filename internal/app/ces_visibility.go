package app

import (
	"strings"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
)

// enqueuePhaseEvent is called by the fresh phase session goroutine. The
// channel is the boundary between runtime observation and the Bubble Tea
// model; it never mutates roster or history state directly.
func (m *Model) enqueuePhaseEvent(e cognition.PhaseEvent) {
	if m.phaseEvents == nil {
		return
	}
	m.phaseEvents <- e
}

// drainPhaseEvents runs on the Bubble Tea goroutine. Result handling drains
// before committing a phase so the observer's terminal event cannot arrive
// after the next resource has been selected.
func (m *Model) drainPhaseEvents() {
	for m.phaseEvents != nil {
		select {
		case e := <-m.phaseEvents:
			m.applyPhaseEvent(e)
		default:
			return
		}
	}
}

func (m *Model) applyPhaseEvent(e cognition.PhaseEvent) {
	if e.RunID == "" || m.cesRunID == "" || e.RunID != m.cesRunID {
		return
	}
	agentID := m.phaseAgentID(e.ResourceID, e.AgentID)
	if agentID == "" {
		return
	}

	item := event.Event{
		Time:      e.Time,
		Source:    strings.ToUpper(agentID),
		Kind:      event.KindStatus,
		Message:   cesTraceMessage(e.Phase, e.Name, ""),
		SessionID: e.SessionID,
		TurnID:    e.TurnID,
		Metadata: map[string]string{
			"ces_event":    e.Name,
			"work_id":      e.WorkID,
			"phase":        e.Phase,
			"resource_id":  e.ResourceID,
			"agent_id":     agentID,
			"phase_run_id": e.RunID,
		},
	}
	for key, value := range e.Fields {
		item.Metadata[key] = value
	}
	if e.Runtime != nil {
		item.Raw = append([]byte(nil), e.Runtime.Raw...)
		if item.SessionID == "" {
			item.SessionID = e.Runtime.SessionID
		}
		if item.TurnID == "" {
			item.TurnID = e.Runtime.TurnID
		}
		if e.Name == cognition.PhaseActivity {
			item.Kind = activityKind(e.Runtime.Kind)
			item.Message = e.Runtime.Summary
			for key, value := range e.Runtime.Metadata {
				item.Metadata["runtime_"+key] = value
			}
		} else {
			item.Message = cesTraceMessage(e.Phase, e.Name, e.Runtime.Summary)
		}
	}
	if item.Message == "" {
		item.Message = strings.ToLower(e.Name)
	}

	state := ghostmodel.AgentActive
	activity := item.Message
	switch e.Name {
	case cognition.PhaseFinished:
		if e.Fields["outcome"] == "error" {
			state = ghostmodel.AgentError
		} else {
			state = ghostmodel.AgentIdle
		}
	case cognition.PhaseSessionFailed, cognition.PhaseTurnFailed, cognition.PhaseTimeout, cognition.PhaseStalled, cognition.PhaseCanceled:
		state = ghostmodel.AgentError
	}
	m.recordProjection(agentID, item, item.Metadata)
	m.dashboard = m.dashboard.UpdateAgent(agentID, state, activity, "")
	m.syncDetail()
}

func (m Model) phaseAgentID(resource, runtimeAgent string) string {
	if m.cognition != nil {
		if agent, ok := m.cognition.Agent(resource); ok {
			return agent
		}
	}
	return runtimeAgent
}

func (m *Model) setCESAgentState(state ghostmodel.AgentState, activity string) {
	agentID := m.phaseAgentID(m.cesResource, "")
	if agentID == "" {
		return
	}
	m.dashboard = m.dashboard.UpdateAgent(agentID, state, activity, "")
	m.syncDetail()
}

func cesTerminalAgentState(status epistemic.WorkStatus) ghostmodel.AgentState {
	if status == epistemic.WorkComplete {
		return ghostmodel.AgentDone
	}
	return ghostmodel.AgentError
}
