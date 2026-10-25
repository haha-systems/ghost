package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/epistemic"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/run"
	"github.com/haha-systems/ghost/internal/runtime"
)

func (m Model) waitRunEvent() tea.Cmd {
	return func() tea.Msg {
		e, ok := <-m.runEvents
		if !ok {
			return nil
		}
		return runEventMsg{event: e}
	}
}
func (m Model) handleRunEvent(e run.Event) (tea.Model, tea.Cmd) {
	if e.Status != nil {
		m.runStatus = *e.Status
		if m.runStatus.State.Task.ID != "" {
			snapshot := cesSnapshot(m.runStatus.State)
			snapshot.Supplement.TerminalReason = m.runStatus.State.Task.TerminalReason
			m = m.SetEpistemicView(m.runStatus.View, m.runStatus.Resource, snapshot)
		}
		m.syncWork()
	}
	if e.CoreEvent != nil {
		m.applyCoreEvent(*e.CoreEvent)
		if e.CoreEvent.Type == "session.state" {
			for i := range m.projection.Agents {
				a := &m.projection.Agents[i]
				if a.ID != e.CoreEvent.AgentID {
					continue
				}
				if session := m.sessions[a.DisplayName]; session != nil {
					switch session.State() {
					case runtime.StateRunning, runtime.StateInterrupting:
						a.Availability = core.AvailabilityBusy
					case runtime.StateFailed:
						a.Availability = core.AvailabilityError
					default:
						a.Availability = core.AvailabilityAvailable
					}
				}
			}
		}
	}
	if e.Entry.Message != "" || e.Entry.Kind != "" {
		m.recordProjection(e.AgentID, e.Entry, e.Entry.Metadata)
	}
	if e.Semantic != nil {
		m.epistemic = m.epistemic.AppendEvent(e.Entry)
	}
	if e.Phase != nil {
		m.applyPhaseEvent(*e.Phase)
	}
	if e.Activity != "" && e.AgentID != "" {
		state := ghostmodel.AgentActive
		switch e.ResourceStatus {
		case epistemic.WorkComplete:
			state = ghostmodel.AgentDone
		case epistemic.WorkIncomplete:
			state = ghostmodel.AgentError
		}
		m.dashboard = m.dashboard.UpdateAgent(e.AgentID, state, e.Activity, "")
		for i := range m.projection.Agents {
			if strings.EqualFold(m.projection.Agents[i].DisplayName, e.AgentID) || (e.CoreEvent != nil && m.projection.Agents[i].ID == e.CoreEvent.AgentID) {
				m.projection.Agents[i].Activity = e.Activity
				m.projection.Agents[i].Availability = core.AvailabilityBusy
				if state == ghostmodel.AgentDone {
					m.projection.Agents[i].Availability = core.AvailabilityAvailable
				} else if state == ghostmodel.AgentError {
					m.projection.Agents[i].Availability = core.AvailabilityError
				}
			}
		}
		m.syncDetail()
	}
	if e.ReplaceResponse != "" {
		m.replaceLatestResponse(e.AgentID, e.ReplaceResponse)
	}
	return m, m.waitRunEvent()
}
