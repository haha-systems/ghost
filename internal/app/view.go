package app

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

func (m Model) View() tea.View {
	var view tea.View
	switch m.screen {
	case ConversationScreen:
		view = tea.NewView(m.renderConversationView())
	case LegacyDashboardScreen:
		view = tea.NewView(m.dashboard.View())
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
	if m.colorProfile > colorprofile.ASCII {
		view.BackgroundColor = lipgloss.Color(m.theme.Colors.Background)
	}
	return view
}
