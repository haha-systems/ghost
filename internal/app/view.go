package app

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m Model) View() tea.View {
	var view tea.View
	if m.screen == AgentDetailScreen {
		view = tea.NewView(m.detail.View())
	} else {
		view = tea.NewView(m.dashboard.View())
	}
	view.AltScreen = true
	view.BackgroundColor = lipgloss.Color(m.theme.Colors.Background)
	return view
}
