package agentdetail

import (
	"fmt"
	"strings"

	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/chrome"
	"github.com/haha-systems/ghost/internal/ui/footer"
	"github.com/haha-systems/ghost/internal/ui/pane"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

const (
	steerLabelWidth = 12
	chromePad       = 2
)

func (m Model) View() string {
	if m.screen.TooSmall {
		return chrome.TooSmall(m.theme, m.width, m.height)
	}

	body := chrome.Columns(m.theme,
		chrome.Sidebar(m.theme, m.sidebarRows(), m.screen.Sidebar, m.screen.ContentRows()),
		m.contentColumn(), m.screen.Sidebar)
	if m.help.ShowAll {
		m.help.SetWidth(maxInt(m.width-2*chromePad, 1))
		body += "\n" + chrome.Rule(m.theme, m.width-2*chromePad) + "\n" + m.help.View(m.keys)
	}
	body += "\n" + footer.Render(m.theme, footer.Hints(m.footerContext()), m.width-2*chromePad)
	return theme.Frame(m.theme, m.width, m.height, body)
}

// footerContext reports what the operator can do from where they are. Interrupt
// is offered only while the agent actually has a turn to stop.
func (m Model) footerContext() footer.Context {
	ctx := footer.Context{Screen: footer.AgentDetail, SubmitKey: m.submitKey}
	switch m.focus {
	case FocusDecisions:
		ctx.Focus = footer.Decisions
		ctx.Following = m.decisions.Follow()
	case FocusActivity:
		ctx.Focus = footer.Activity
		ctx.Following = m.activity.Follow()
	default:
		ctx.Focus = footer.Steering
		ctx.HasDraft = m.input.Value() != ""
		ctx.CanInterrupt = m.agent.State == ghostmodel.AgentActive
	}
	return ctx
}

// contentColumn stacks decisions over activity over the steering editor.
func (m Model) contentColumn() string {
	width := m.screen.Content
	steering := chrome.Fit(m.input.View(), maxInt(width-steerLabelWidth, 1), m.screen.Steering)

	label := style(m.theme, m.theme.Colors.Accent).Render("STEER ") +
		style(m.theme, m.theme.Colors.AccentHot).Render(m.theme.Symbols.Prompt)
	steerLines := strings.Split(steering, "\n")
	for i, line := range steerLines {
		if i == 0 {
			steerLines[i] = chrome.Pad(label, steerLabelWidth) + line
			continue
		}
		steerLines[i] = strings.Repeat(" ", steerLabelWidth) + line
	}

	return strings.Join([]string{
		chrome.PaneTitle(m.theme, "DECISIONS", NewBadge(m.decisions), m.focus == FocusDecisions, width),
		chrome.Fit(m.decisions.View(), width, m.screen.Panes[0]),
		chrome.FocusRule(m.theme, width, m.focus == FocusDecisions),
		chrome.PaneTitle(m.theme, "ACTIVITY", NewBadge(m.activity), m.focus == FocusActivity, width),
		chrome.Fit(m.activity.View(), width, m.screen.Panes[1]),
		chrome.FocusRule(m.theme, width, m.focus == FocusActivity),
		strings.Join(steerLines, "\n"),
	}, "\n")
}

func (m Model) sidebarRows() []chrome.Row {
	glyph, colour := m.stateStyle()
	rows := []chrome.Row{
		{Label: style(m.theme, m.theme.Colors.AccentHot).Bold(true).Render("GHOST / " + m.agent.Callsign)},
		{Rule: true},
		{Label: "STATUS", Value: glyph + " " + strings.ToUpper(string(m.agent.State)), Style: colour},
		{Label: "CLIENT", Value: display(m.agent.Client)},
		{Label: "MODEL", Value: display(m.agent.Model)},
		{Label: "SESSION", Value: display(m.agent.SessionID)},
		{Label: "RUNTIME", Value: display(m.agent.Runtime)},
		{Label: "QAC", Value: display(strings.ToUpper(m.work))},
		{Rule: true},
		{Label: "ACTIVITY", Heading: true},
		{Label: display(m.agent.Activity)},
	}
	return rows
}

func (m Model) stateStyle() (string, string) {
	switch string(m.agent.State) {
	case "active":
		return m.theme.Symbols.Active, m.theme.Colors.Accent
	case "waiting":
		return m.theme.Symbols.Important, m.theme.Colors.Warning
	case "done":
		return m.theme.Symbols.Complete, m.theme.Colors.Success
	case "error":
		return m.theme.Symbols.Error, m.theme.Colors.Error
	default:
		return m.theme.Symbols.Idle, m.theme.Colors.TextMuted
	}
}

func display(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

// NewBadge renders the indicator shown while a pane has stopped following.
// A pane that is following has nothing outstanding, so it shows nothing.
func NewBadge(p pane.Pane) string {
	if p.Follow() || p.NewCount() == 0 {
		return ""
	}
	return fmt.Sprintf("%s %d NEW", "\u2193", p.NewCount())
}
