package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/haha-systems/ghost/internal/epistemic"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/run"
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
		m.syncDetail()
	}
	if e.ReplaceResponse != "" {
		m.replaceLatestResponse(e.AgentID, e.ReplaceResponse)
	}
	return m, m.waitRunEvent()
}
func (m Model) cesActive() bool { return m.runStatus.Active }
