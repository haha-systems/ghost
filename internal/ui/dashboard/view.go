package dashboard

import (
	"fmt"
	"strings"

	"github.com/haha-systems/ghost/internal/ui/chrome"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

const steerLabelWidth = 12

func (m Model) View() string {
	if m.screen.TooSmall {
		return chrome.TooSmall(m.theme, m.width, m.height)
	}

	content := m.contentColumn()
	sidebar := chrome.Sidebar(m.theme, m.sidebarRows(), m.screen.Sidebar, m.contentRows())
	body := chrome.Columns(m.theme, sidebar, content, m.screen.Sidebar)
	if m.help.ShowAll {
		m.help.SetWidth(maxInt(m.width-2*chromePad, 1))
		body += "\n" + chrome.Rule(m.theme, m.width-2*chromePad) + "\n" + m.help.View(m.keys)
	}
	return theme.Frame(m.theme, m.width, m.height, body)
}

const chromePad = 2

// contentRows is the height of the content column, matching the sidebar so the
// two columns stay flush.
func (m Model) contentRows() int {
	rows := m.screen.Steering + 1 + 1
	for _, p := range m.screen.Panes {
		rows += p + 1
	}
	return rows
}

// contentColumn stacks the live log above the steering editor.
func (m Model) contentColumn() string {
	width := m.screen.Content
	log := chrome.Fit(m.viewport.View(), width, m.screen.Panes[0])
	steering := chrome.Fit(m.input.View(), maxInt(width-steerLabelWidth, 1), m.screen.Steering)

	label := style(m.theme, m.theme.Colors.Accent).Render("STEER ALL ") +
		style(m.theme, m.theme.Colors.AccentHot).Render(m.theme.Symbols.Prompt)
	if m.focus == FocusSteering {
		label = style(m.theme, m.theme.Colors.AccentHot).Bold(true).Render("STEER ALL ") +
			style(m.theme, m.theme.Colors.AccentHot).Render(m.theme.Symbols.Prompt)
	}

	// The label sits on the editor's first row; later rows are indented to it.
	steerLines := strings.Split(steering, "\n")
	for i, line := range steerLines {
		if i == 0 {
			steerLines[i] = chrome.Pad(label, steerLabelWidth) + line
			continue
		}
		steerLines[i] = strings.Repeat(" ", steerLabelWidth) + line
	}

	return strings.Join([]string{
		chrome.PaneTitle(m.theme, "EVENT STREAM", m.focus == FocusEvents, width),
		log,
		chrome.Rule(m.theme, width),
		strings.Join(steerLines, "\n"),
	}, "\n")
}

func (m Model) sidebarRows() []chrome.Row {
	rows := []chrome.Row{
		{Label: style(m.theme, m.theme.Colors.AccentHot).Bold(true).Render("GHOST") +
			style(m.theme, m.theme.Colors.TextMuted).Render("  BLOODWIRE")},
		{Rule: true},
		{Label: "AGENTS", Value: fmt.Sprintf("%d", len(m.agents))},
		{Label: "QAC", Value: m.qacValue(), Style: m.qacColour()},
	}
	if m.work.Enabled && m.work.Goal != "" {
		rows = append(rows, chrome.Row{Label: "GOAL", Value: collapse(m.work.Goal)})
	}
	rows = append(rows, chrome.Row{Rule: true}, chrome.Row{Label: "ROSTER", Heading: true})

	for i, agent := range m.agents {
		glyph, colour := m.statusGlyph(agent.State)
		cursor := " "
		if i == m.selected {
			cursor = m.theme.Symbols.Cursor
			if m.focus == FocusAgents {
				colour = m.theme.Colors.Accent
			}
		}
		label := fmt.Sprintf("%s %s %s", cursor, glyph, agent.Callsign)
		rows = append(rows, chrome.Row{
			Label: style(m.theme, colour).Render(chrome.Pad(label, 14)) +
				style(m.theme, m.theme.Colors.TextMuted).Render(strings.ToUpper(string(agent.State))),
		})
	}
	return rows
}

func (m Model) qacValue() string {
	if !m.work.Enabled {
		return "OFF"
	}
	value := "ON"
	if m.work.State != "" {
		value = strings.ToUpper(m.work.State)
	}
	if m.work.Owner != "" {
		value += " / " + strings.ToUpper(m.work.Owner)
	}
	return value
}

func (m Model) qacColour() string {
	if !m.work.Enabled {
		return m.theme.Colors.TextMuted
	}
	switch strings.ToLower(m.work.State) {
	case "done":
		return m.theme.Colors.Success
	case "paused":
		return m.theme.Colors.Warning
	default:
		return m.theme.Colors.Accent
	}
}
