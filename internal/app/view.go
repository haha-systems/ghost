package app

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m Model) View() tea.View {
	var view tea.View
	switch m.screen {
	case AgentDetailScreen:
		view = tea.NewView(m.detail.View())
	case EpistemicScreen:
		view = tea.NewView(m.epistemic.View())
	default:
		view = tea.NewView(m.dashboard.View())
	}
	view.AltScreen = true
	// Wheel events are routed by pointer position, which requires the terminal
	// to report them in the first place.
	view.MouseMode = tea.MouseModeCellMotion
	view.BackgroundColor = lipgloss.Color(m.theme.Colors.Background)
	return view
}
