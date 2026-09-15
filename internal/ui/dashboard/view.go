package dashboard

import (
	"fmt"
	"strings"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/ui/chrome"
	"github.com/haha-systems/ghost/internal/ui/footer"
	"github.com/haha-systems/ghost/internal/ui/pane"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

const steerLabelWidth = 12

func (m Model) View() string {
	if m.screen.TooSmall {
		return chrome.TooSmall(m.theme, m.width, m.height)
	}

	content := m.contentColumn()
	sidebar := chrome.Sidebar(m.theme, m.sidebarRows(m.screen.ContentRows()), m.screen.Sidebar, m.screen.ContentRows())
	body := chrome.Columns(m.theme, sidebar, content, m.screen.Sidebar)
	if m.help.ShowAll {
		m.help.SetWidth(maxInt(m.width-2*chromePad, 1))
		body += "\n" + chrome.Rule(m.theme, m.width-2*chromePad) + "\n" + m.help.View(m.keys)
	}
	body += "\n" + footer.Render(m.theme, footer.Hints(m.footerContext()), m.width-2*chromePad)
	return theme.Frame(m.theme, m.width, m.height, body)
}

// footerContext reports what the operator can do from where they are.
func (m Model) footerContext() footer.Context {
	ctx := footer.Context{Screen: footer.Dashboard, SubmitKey: m.submitKey}
	switch m.focus {
	case FocusEvents:
		ctx.Focus = footer.Stream
		ctx.Following = m.stream.Follow()
	case FocusSteering:
		ctx.Focus = footer.Steering
		ctx.HasDraft = m.input.Value() != ""
	default:
		ctx.Focus = footer.Roster
	}
	return ctx
}

const chromePad = 2

// contentColumn stacks the live log above the steering editor.
func (m Model) contentColumn() string {
	width := m.screen.Content
	log := chrome.Fit(m.stream.View(), width, m.screen.Panes[0])
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
		chrome.PaneTitle(m.theme, "EVENT STREAM", NewBadge(m.stream), m.focus == FocusEvents, width),
		log,
		chrome.FocusRule(m.theme, width, m.focus == FocusEvents),
		strings.Join(steerLines, "\n"),
	}, "\n")
}

func (m Model) sidebarRows(height int) []chrome.Row {
	work, phase, cognition, goal := m.presentationStatus()
	rows := []chrome.Row{
		{Label: style(m.theme, m.theme.Colors.AccentHot).Bold(true).Render("GHOST") +
			style(m.theme, m.theme.Colors.TextMuted).Render("  BLOODWIRE")},
		{Rule: true},
		{Label: "WORK", Value: work, Style: m.workColour()},
		{Label: "PHASE", Value: phase, Style: m.theme.Colors.Accent},
		{Label: "COGNITION", Value: cognition, Style: m.theme.Colors.Accent},
	}
	if goal != "" {
		rows = append(rows, chrome.Row{Label: "GOAL", Value: collapse(goal)})
	}
	if m.operator != nil {
		rows = append(rows, chrome.Row{Label: "PHASE RAIL", Heading: true})
		if height < 27 {
			rows = append(rows, chrome.Row{Label: "RAIL", Value: m.compactRail()})
		} else {
			rows = append(rows, m.phaseRows()...)
		}
		if m.operator.ReopenCount > 0 {
			rows = append(rows, chrome.Row{Label: "REOPEN", Value: fmt.Sprintf("%d", m.operator.ReopenCount), Style: m.theme.Colors.Warning})
		}
		rows = append(rows,
			chrome.Row{Label: "FRONTIER", Heading: true},
			chrome.Row{Label: "HYP", Value: m.frontierValue(m.operator.LeadingHypothesis)},
			chrome.Row{Label: "FRAME", Value: m.frontierValue(m.operator.ActiveFrame)},
			chrome.Row{Label: "UNKNOWN", Value: fmt.Sprintf("%d", m.operator.OpenUnknownCount)},
			chrome.Row{Label: "CONTRA", Value: fmt.Sprintf("%d", m.operator.ContradictionCount), Style: m.contradictionColour()},
		)
		if transition := m.operator.LastTransition; transition != nil {
			route := transitionRoute(*transition)
			if route != "" {
				rows = append(rows, chrome.Row{Label: "LAST", Value: route})
			}
			if transition.Reason != "" {
				label := "REASON"
				if m.supplement.TerminalReason != "" {
					label = "LAST WHY"
				}
				if height < 27 {
					rows = append(rows, chrome.Row{Label: label, Value: collapse(transition.Reason), Style: m.theme.Colors.Warning})
				} else {
					rows = appendWrappedRows(rows, label, transition.Reason, m.theme.Colors.Warning)
				}
			}
		}
		if m.supplement.TerminalReason != "" {
			if height < 27 {
				rows = append(rows, chrome.Row{Label: "REASON", Value: collapse(m.supplement.TerminalReason), Style: m.theme.Colors.Warning})
			} else {
				rows = appendWrappedRows(rows, "REASON", m.supplement.TerminalReason, m.theme.Colors.Warning)
			}
		}
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
		rows = append(rows, chrome.Row{Label: style(m.theme, colour).Render(chrome.Pad(label, 14)) +
			style(m.theme, m.theme.Colors.TextMuted).Render(strings.ToUpper(string(agent.State)))})
	}
	return rows
}

func appendWrappedRows(rows []chrome.Row, label, value, colour string) []chrome.Row {
	for i, line := range wrapWords(collapse(value), 20) {
		rowLabel := ""
		if i == 0 {
			rowLabel = label
		}
		rows = append(rows, chrome.Row{Label: rowLabel, Value: line, Style: colour})
	}
	return rows
}

func (m Model) presentationStatus() (work, phase, cognition, goal string) {
	if m.operator != nil {
		work = strings.ToUpper(string(m.operator.WorkStatus))
		phase = strings.ToUpper(string(m.operator.Phase))
		goal = m.operator.Goal
	} else {
		if m.work.Enabled && (m.work.Goal != "" || m.work.State != "") {
			// QAC controls resources. Until CES reports an outcome, its stop or
			// paused state cannot turn the user task into COMPLETE.
			work = "ACTIVE"
		}
		goal = m.work.Goal
	}
	if work == "" {
		work = "—"
	}
	if phase == "" {
		phase = "—"
	}
	if m.operator != nil {
		cognition = strings.ToUpper(m.cognition)
	} else {
		cognition = strings.ToUpper(m.work.Owner)
	}
	if cognition == "" {
		cognition = "—"
	}
	return work, phase, cognition, goal
}

func (m Model) workColour() string {
	if m.operator == nil {
		if m.work.Enabled {
			return m.theme.Colors.Accent
		}
		return m.theme.Colors.TextMuted
	}
	switch m.operator.WorkStatus {
	case epistemic.WorkComplete:
		return m.theme.Colors.Success
	case epistemic.WorkIncomplete:
		return m.theme.Colors.Warning
	default:
		return m.theme.Colors.Accent
	}
}

func (m Model) contradictionColour() string {
	if m.operator != nil && m.operator.ContradictionCount > 0 {
		return m.theme.Colors.Warning
	}
	return m.theme.Colors.TextMuted
}

var phaseOrder = []epistemic.Phase{epistemic.PhaseTriage, epistemic.PhaseAbduce, epistemic.PhaseFrame, epistemic.PhaseExecute, epistemic.PhaseClose}

func (m Model) phaseRows() []chrome.Row {
	rows := make([]chrome.Row, 0, len(phaseOrder))
	for _, phase := range phaseOrder {
		marker, colour := m.phaseMarker(phase)
		rows = append(rows, chrome.Row{Label: style(m.theme, colour).Render(marker + " " + strings.ToUpper(string(phase)))})
	}
	return rows
}

func (m Model) compactRail() string {
	var rail []string
	for _, phase := range phaseOrder {
		marker, _ := m.phaseMarker(phase)
		short := map[epistemic.Phase]string{
			epistemic.PhaseTriage: "T", epistemic.PhaseAbduce: "A", epistemic.PhaseFrame: "F",
			epistemic.PhaseExecute: "E", epistemic.PhaseClose: "C",
		}[phase]
		rail = append(rail, marker+short)
	}
	return strings.Join(rail, " ")
}

func (m Model) phaseMarker(phase epistemic.Phase) (string, string) {
	if m.operator != nil && m.operator.Phase == phase {
		return "▶", m.theme.Colors.AccentHot
	}
	if m.operator != nil {
		for _, invalidated := range m.supplement.InvalidatedPhases {
			if invalidated == phase {
				return "!", m.theme.Colors.Warning
			}
		}
	}
	if m.operator != nil && phasePosition(phase) < phasePosition(m.operator.Phase) {
		return "✓", m.theme.Colors.Success
	}
	return "·", m.theme.Colors.TextMuted
}

func phasePosition(phase epistemic.Phase) int {
	for i, current := range phaseOrder {
		if current == phase {
			return i
		}
	}
	return -1
}

func (m Model) frontierValue(object *epistemic.ObjectSummary) string {
	if object == nil {
		return "—"
	}
	if strings.TrimSpace(object.Summary) != "" {
		return object.Summary
	}
	return object.Label
}

func transitionRoute(transition epistemic.TransitionSummary) string {
	if transition.From == "" || transition.To == "" {
		return ""
	}
	return strings.ToUpper(string(transition.From)) + " -> " + strings.ToUpper(string(transition.To))
}

// NewBadge renders the indicator shown while a pane has stopped following.
// A pane that is following has nothing outstanding, so it shows nothing.
func NewBadge(p pane.Pane) string {
	if p.Follow() || p.NewCount() == 0 {
		return ""
	}
	return fmt.Sprintf("%s %d NEW", "\u2193", p.NewCount())
}
