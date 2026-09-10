package dashboard

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

func (m Model) View() string {
	if m.width > 0 && (m.width < 80 || m.height > 0 && m.height < 24) {
		return theme.Frame(m.theme, m.width, m.height, style(m.theme, m.theme.Colors.TextMuted).Render("Terminal too small.\nMinimum recommended size: 80x24."))
	}

	muted := style(m.theme, m.theme.Colors.TextMuted)
	accent := style(m.theme, m.theme.Colors.Accent)
	hot := style(m.theme, m.theme.Colors.AccentHot)
	border := style(m.theme, m.theme.Colors.Border)
	title := hot.Bold(true).Render("GHOST") + muted.Render("  /  BLOODWIRE")
	header := lipgloss.JoinHorizontal(lipgloss.Top,
		accent.Bold(true).Render("SYSTEM"), muted.Render(fmt.Sprintf("   %d AGENTS", len(m.agents))),
		muted.Render("                                      "), accent.Render("QAC"), muted.Render("  OFF"),
	)

	steeringLabel := accent.Render("STEER ALL ") + hot.Render(m.theme.Symbols.Prompt)
	steering := steeringLabel + " " + m.input.View()

	activityWidth := maxInt(m.width-54, 12)
	rows := []string{muted.Bold(true).Render("AGENT        CLIENT       STATE        ACTIVITY")}
	for i, agent := range m.agents {
		glyph, stateStyle := m.status(agent.State)
		cursor := " "
		if i == m.selected {
			cursor = m.theme.Symbols.Cursor
		}
		row := fmt.Sprintf("%s %s %-11s %-12s %-11s %s", cursor, glyph, agent.Callsign, agent.Client, strings.ToUpper(string(agent.State)), truncate(agent.Activity, activityWidth))
		if i == m.selected {
			row = accent.Render(row)
		} else {
			row = stateStyle.Render(row)
		}
		rows = append(rows, row)
	}
	agents := strings.Join(rows, "\n")

	eventPrefix := " "
	if m.focus == FocusEvents {
		eventPrefix = m.theme.Symbols.Cursor
	}
	eventsTitle := muted.Bold(true).Render(eventPrefix + " EVENT STREAM")
	events := m.viewport.View()
	footer := ""
	if m.help.ShowAll {
		m.help.SetWidth(maxInt(m.width-6, 1))
		footer = "\n" + m.help.View(m.keys)
	}

	content := strings.Join([]string{
		title,
		"",
		header,
		"",
		steering,
		border.Render(strings.Repeat("─", maxInt(m.width-6, 1))),
		agents,
		border.Render(strings.Repeat("─", maxInt(m.width-6, 1))),
		eventsTitle,
		events,
		footer,
	}, "\n")
	return theme.Frame(m.theme, m.width, m.height, content)
}

func (m Model) status(state model.AgentState) (string, lipgloss.Style) {
	switch state {
	case model.AgentActive:
		return m.theme.Symbols.Active, style(m.theme, m.theme.Colors.Accent)
	case model.AgentWaiting:
		return m.theme.Symbols.Important, style(m.theme, m.theme.Colors.Warning)
	case model.AgentDone:
		return m.theme.Symbols.Complete, style(m.theme, m.theme.Colors.Success)
	case model.AgentError:
		return m.theme.Symbols.Error, style(m.theme, m.theme.Colors.Error)
	default:
		return m.theme.Symbols.Idle, style(m.theme, m.theme.Colors.TextMuted)
	}
}
