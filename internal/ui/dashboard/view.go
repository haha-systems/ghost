package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
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

func wrapWords(value string, width int) []string {
	if width < 1 {
		return []string{value}
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(value) {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if line != "" && ansi.StringWidth(candidate) > width {
			lines = append(lines, line)
			line = word
		} else {
			line = candidate
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// eventLine renders one stream row, clipped to the pane so a long summary can
// never reflow the columns beneath it.
func (m *Model) eventLine(item event.Event, width int) string {
	stamp := style(m.theme, m.theme.Colors.TextMuted).Render(item.Time.Format("15:04:05"))
	if semanticKind(item.Kind) {
		kindText := strings.ToUpper(string(item.Kind))
		sourceWidth := maxInt(ansi.StringWidth(item.Source), 3)
		kindWidth := ansi.StringWidth(kindText)
		source := style(m.theme, m.theme.Colors.Accent).Bold(true).Render(chrome.Pad(item.Source, sourceWidth))
		kind := style(m.theme, kindColour(m.theme, item.Kind)).Render(chrome.Pad(kindText, kindWidth))
		prefix := 8 + 1 + sourceWidth + 1 + kindWidth + 1
		if headline, detail, ok := semanticBlock(item); ok {
			prefixText := strings.Join([]string{stamp, source, kind}, " ") + " "
			messageWidth := maxInt(width-prefix, 8)
			rows := []string{prefixText + style(m.theme, m.theme.Colors.Text).Render(chrome.Truncate(headline, messageWidth))}
			if detail != "" {
				for _, line := range wrapWords(detail, messageWidth) {
					rows = append(rows, strings.Repeat(" ", prefix)+style(m.theme, m.theme.Colors.Warning).Render(chrome.Truncate(line, messageWidth)))
				}
			}
			return strings.Join(rows, "\n")
		}
		message := chrome.Truncate(collapse(semanticMessage(item)), maxInt(width-prefix, 8))
		return strings.Join([]string{stamp, source, kind, style(m.theme, m.theme.Colors.Text).Render(message)}, " ")
	}
	source := style(m.theme, m.theme.Colors.Accent).Bold(true).Render(chrome.Pad(item.Source, 8))
	kind := style(m.theme, kindColour(m.theme, item.Kind)).Render(chrome.Pad(string(item.Kind), 9))
	const prefix = 8 + 2 + 8 + 2 + 9 + 2
	message := chrome.Truncate(collapse(semanticMessage(item)), maxInt(width-prefix, 8))
	return strings.Join([]string{stamp, source, kind, style(m.theme, m.theme.Colors.Text).Render(message)}, "  ")
}

func semanticBlock(item event.Event) (headline, detail string, ok bool) {
	metadata := item.Metadata
	get := func(key string) string { return strings.TrimSpace(metadata[key]) }
	switch item.Kind {
	case event.KindContradict:
		target := strings.ToUpper(get("target_kind"))
		if target == "" {
			target = "CONTRADICTION"
		} else {
			target += " INVALIDATED"
		}
		relation := ""
		if get("source_alias") != "" || get("target_alias") != "" {
			relation = strings.TrimSpace(get("source_alias") + " contradicts " + get("target_alias"))
		}
		headline = target
		if relation != "" {
			headline += "  " + relation
		}
		detail = get("reason")
		if detail == "" {
			detail = item.Message
		}
		return headline, detail, true
	case event.KindReopen:
		from, to := get("from"), get("to")
		if from != "" && to != "" {
			headline = strings.ToUpper(from) + " -> " + strings.ToUpper(to)
		}
		count := get("reopen_count")
		if count != "" {
			if limit := get("reopen_limit"); limit != "" {
				count += "/" + limit
			}
			headline = strings.TrimSpace(headline + "  REOPEN " + count)
		}
		detail = get("reason")
		if headline == "" && detail == "" {
			return item.Message, "", true
		}
		return headline, detail, true
	case event.KindIncomplete:
		detail = get("reason")
		if detail == "" {
			detail = item.Message
		}
		return "WORK INCOMPLETE", detail, true
	default:
		return "", "", false
	}
}

func semanticKind(kind event.Kind) bool {
	switch kind {
	case event.KindPhase, event.KindHypothesis, event.KindReject, event.KindFrame, event.KindAction,
		event.KindVerify, event.KindContradict, event.KindReopen, event.KindComplete, event.KindIncomplete:
		return true
	default:
		return false
	}
}

func semanticMessage(item event.Event) string {
	metadata := item.Metadata
	get := func(key string) string { return strings.TrimSpace(metadata[key]) }
	join := func(parts ...string) string {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				out = append(out, strings.TrimSpace(part))
			}
		}
		return strings.Join(out, "  ")
	}
	summary := get("summary")
	if summary == "" {
		summary = item.Message
	}
	switch item.Kind {
	case event.KindPhase:
		if from, to := get("from"), get("to"); from != "" && to != "" {
			return strings.ToUpper(from) + " -> " + strings.ToUpper(to)
		}
	case event.KindHypothesis:
		if get("status") == "leading" {
			return "leading: " + summary
		}
	case event.KindReject:
		return "hypothesis rejected: " + summary
	case event.KindFrame:
		return "frame activated: " + summary
	case event.KindAction:
		return "action: " + summary
	case event.KindVerify:
		return "verification: " + summary
	case event.KindContradict:
		label := strings.ToUpper(get("target_kind"))
		if label != "" {
			label += " INVALIDATED"
		}
		relation := ""
		if get("source_alias") != "" || get("target_alias") != "" {
			relation = get("source_alias") + " contradicts " + get("target_alias")
		}
		return join(label, relation, get("reason"), item.Message)
	case event.KindReopen:
		route := ""
		if from, to := get("from"), get("to"); from != "" && to != "" {
			route = strings.ToUpper(from) + " -> " + strings.ToUpper(to)
		}
		count := get("reopen_count")
		if count != "" {
			if limit := get("reopen_limit"); limit != "" {
				count += "/" + limit
			}
			count = "REOPEN " + count
		}
		if route != "" || count != "" || get("reason") != "" {
			return join(route, count, get("reason"))
		}
		return item.Message
	case event.KindComplete:
		return join("WORK COMPLETE", item.Message)
	case event.KindIncomplete:
		return join("WORK INCOMPLETE", get("reason"), item.Message)
	}
	return item.Message
}

// collapse folds a multi-line summary onto the stream's single row.
func collapse(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")
	return strings.Join(strings.Fields(r.Replace(s)), " ")
}

// kindColour gives each normalized event kind its own weight in the stream.
func kindColour(th theme.Theme, kind event.Kind) string {
	switch kind {
	case event.KindError:
		return th.Colors.Error
	case event.KindContradict, event.KindReopen, event.KindIncomplete:
		return th.Colors.Warning
	case event.KindComplete:
		return th.Colors.Success
	case event.KindResponse:
		return th.Colors.AccentHot
	case event.KindQAC:
		return th.Colors.Warning
	case event.KindSteering:
		return th.Colors.Accent
	case event.KindCommand, event.KindTool, event.KindPhase, event.KindHypothesis, event.KindReject, event.KindFrame, event.KindAction, event.KindVerify:
		return th.Colors.Accent
	case event.KindFile:
		return th.Colors.Success
	default:
		return th.Colors.AccentDim
	}
}

func (m Model) statusGlyph(state ghostmodel.AgentState) (string, string) {
	switch state {
	case ghostmodel.AgentActive:
		return m.theme.Symbols.Active, m.theme.Colors.Accent
	case ghostmodel.AgentWaiting:
		return m.theme.Symbols.Important, m.theme.Colors.Warning
	case ghostmodel.AgentDone:
		return m.theme.Symbols.Complete, m.theme.Colors.Success
	case ghostmodel.AgentError:
		return m.theme.Symbols.Error, m.theme.Colors.Error
	default:
		return m.theme.Symbols.Idle, m.theme.Colors.TextMuted
	}
}
