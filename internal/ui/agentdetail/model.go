package agentdetail

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

type Focus int

const (
	FocusSteering Focus = iota
	FocusLog
)

type BackMsg struct{}

type SteeringSubmittedMsg struct {
	AgentID string
	Text    string
}
type InterruptMsg struct{ AgentID string }
type LogEntry struct{ Type, Text string }

type Model struct {
	theme       theme.Theme
	keys        keymap.KeyMap
	help        help.Model
	agent       ghostmodel.Agent
	logs        []LogEntry
	lastMessage bool

	input    textinput.Model
	viewport viewport.Model
	follow   bool
	focus    Focus
	width    int
	height   int
}

func New(th theme.Theme, keys keymap.KeyMap) Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "steer this agent..."
	in.CharLimit = 240
	in.SetStyles(inputStyles(th))
	h := help.New()
	h.Styles = helpStyles(th)
	return Model{
		theme: th, keys: keys, help: h, input: in, focus: FocusSteering,
		logs: []LogEntry{{Type: "event", Text: "Searching references..."}, {Type: "event", Text: "Running tests..."}, {Type: "event", Text: "Inspecting result..."}}, viewport: viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)), follow: true,
	}
}

func (m Model) SetAgent(agent ghostmodel.Agent) Model {
	m.agent = agent
	m.input.Prompt = ""
	m.input.Reset()
	_ = m.input.Focus()
	m.focus = FocusSteering
	return m
}

func (m Model) Agent() ghostmodel.Agent  { return m.agent }
func (m Model) ClearLogs() Model         { m.logs = nil; m.lastMessage = false; m.setLogContent(); return m }
func (m Model) AddLog(line string) Model { return m.AddTypedLog("event", line) }
func (m Model) AddTypedLog(kind, line string) Model {
	if line != "" {
		m.logs = append(m.logs, LogEntry{Type: kind, Text: line})
		m.lastMessage = false
		if len(m.logs) > 200 {
			m.logs = m.logs[len(m.logs)-200:]
		}
	}
	m.setLogContent()
	if m.follow {
		m.viewport.GotoBottom()
	}
	return m
}
func (m Model) AppendLog(text string) Model {
	if text == "" {
		return m
	}
	if len(m.logs) == 0 || !m.lastMessage {
		m.logs = append(m.logs, LogEntry{Type: "response", Text: text})
		m.lastMessage = true
	} else {
		m.logs[len(m.logs)-1].Text += text
	}
	m.setLogContent()
	if m.follow {
		m.viewport.GotoBottom()
	}
	return m
}

func (m Model) InputFocused() bool { return m.focus == FocusSteering }

func (m Model) Focus() Focus { return m.focus }

func (m Model) InputValue() string { return m.input.Value() }

func (m Model) SetSize(width, height int) Model {
	m.width, m.height = maxInt(width, 0), maxInt(height, 0)
	m.input.SetWidth(maxInt(width-20, 1))
	m.viewport.SetWidth(maxInt(width-6, 1))
	m.updateViewportHeight()
	m.setLogContent()
	if m.follow {
		m.viewport.GotoBottom()
	}
	wasFocused := m.input.Focused()
	if !wasFocused {
		_ = m.input.Focus()
	}
	m.input, _ = m.input.Update(tea.WindowSizeMsg{Width: width, Height: height})
	if !wasFocused {
		m.input.Blur()
	}
	return m
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyPressMsg)
	if isKey {
		if key.Matches(keyMsg, m.keys.Escape) {
			return m, func() tea.Msg { return BackMsg{} }
		}
		if key.Matches(keyMsg, m.keys.Interrupt) && m.agent.ID != "" {
			return m, func() tea.Msg { return InterruptMsg{AgentID: m.agent.ID} }
		}
	}

	if m.focus == FocusSteering {
		if isKey && key.Matches(keyMsg, m.keys.Tab) {
			m.input.Blur()
			m.focus = FocusLog
			return m, nil
		}
		if isKey && key.Matches(keyMsg, m.keys.Enter) {
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			agentID := m.agent.ID
			m.input.Reset()
			return m, func() tea.Msg { return SteeringSubmittedMsg{AgentID: agentID, Text: text} }
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	if isKey && key.Matches(keyMsg, m.keys.Help) {
		m.help.ShowAll = !m.help.ShowAll
		m.updateViewportHeight()
		return m, nil
	}

	if isKey && key.Matches(keyMsg, m.keys.Tab) {
		m.focus = FocusSteering
		return m, m.input.Focus()
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	m.follow = m.viewport.ScrollPercent() >= 0.999
	return m, cmd
}

func (m Model) View() string {
	if m.width > 0 && (m.width < 80 || m.height > 0 && m.height < 24) {
		return theme.Frame(m.theme, m.width, m.height, style(m.theme, m.theme.Colors.TextMuted).Render("Terminal too small.\nMinimum recommended size: 80x24."))
	}

	muted := style(m.theme, m.theme.Colors.TextMuted)
	textStyle := style(m.theme, m.theme.Colors.Text)
	accent := style(m.theme, m.theme.Colors.Accent)
	hot := style(m.theme, m.theme.Colors.AccentHot)
	border := style(m.theme, m.theme.Colors.Border)

	title := hot.Bold(true).Render("GHOST / "+m.agent.Callsign) + muted.Render("                                      "+m.agent.Client)
	status := strings.Join([]string{
		fmt.Sprintf("STATUS     %s", strings.ToUpper(string(m.agent.State))),
		fmt.Sprintf("RUNTIME    %s", m.agent.Runtime),
		fmt.Sprintf("MODEL      %s", m.agent.Model),
		"MEMORY     —",
		"QAC        OFF",
	}, "\n")
	logs := m.viewport.View()
	steering := accent.Render("STEER ") + hot.Render(m.agent.Callsign) + accent.Render(" "+m.theme.Symbols.Prompt) + " " + m.input.View()
	footer := ""
	if m.help.ShowAll {
		m.help.SetWidth(maxInt(m.width-6, 1))
		footer = "\n" + m.help.View(m.keys)
	}

	content := strings.Join([]string{
		title,
		"",
		textStyle.Render(status),
		"",
		border.Render(strings.Repeat("─", maxInt(m.width-6, 1))),
		muted.Bold(true).Render("LIVE LOG"),
		logs,
		border.Render(strings.Repeat("─", maxInt(m.width-6, 1))),
		steering,
		footer,
	}, "\n")
	return theme.Frame(m.theme, m.width, m.height, content)
}

func (m *Model) setLogContent() {
	if len(m.logs) == 0 {
		m.viewport.SetContent("No activity yet.")
		return
	}
	dim := style(m.theme, m.theme.Colors.TextMuted)
	text := style(m.theme, m.theme.Colors.Text)
	lines := make([]string, 0, len(m.logs))
	for _, entry := range m.logs {
		lines = append(lines, dim.Render(entry.Type)+" "+text.Render(entry.Text))
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
}
func (m *Model) updateViewportHeight() {
	extra := 0
	if m.help.ShowAll {
		extra = 3
	}
	m.viewport.SetHeight(maxInt(m.height-16-extra, 1))
}

func style(th theme.Theme, color string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func inputStyles(th theme.Theme) textinput.Styles {
	styles := textinput.DefaultDarkStyles()
	textStyle := style(th, th.Colors.Text)
	mutedStyle := style(th, th.Colors.TextMuted)
	styles.Focused.Text = textStyle
	styles.Focused.Placeholder = mutedStyle
	styles.Focused.Suggestion = mutedStyle
	styles.Blurred.Text = textStyle
	styles.Blurred.Placeholder = mutedStyle
	styles.Blurred.Suggestion = mutedStyle
	styles.Cursor.Color = lipgloss.Color(th.Colors.AccentHot)
	return styles
}

func helpStyles(th theme.Theme) help.Styles {
	styles := help.DefaultDarkStyles()
	styles.ShortKey = style(th, th.Colors.Accent)
	styles.ShortDesc = style(th, th.Colors.TextMuted)
	styles.FullKey = style(th, th.Colors.Accent)
	styles.FullDesc = style(th, th.Colors.TextMuted)
	styles.ShortSeparator = style(th, th.Colors.Border)
	styles.FullSeparator = style(th, th.Colors.Border)
	styles.Ellipsis = style(th, th.Colors.TextMuted)
	return styles
}
