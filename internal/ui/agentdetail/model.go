package agentdetail

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
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

type Model struct {
	theme theme.Theme
	keys  keymap.KeyMap
	help  help.Model
	agent ghostmodel.Agent
	logs  []string

	input  textinput.Model
	focus  Focus
	width  int
	height int
}

func New(th theme.Theme, keys keymap.KeyMap) Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "steer selected agent..."
	in.CharLimit = 240
	in.SetStyles(inputStyles(th))
	h := help.New()
	h.Styles = helpStyles(th)
	return Model{
		theme: th, keys: keys, help: h, input: in, focus: FocusSteering,
		logs: []string{"Searching references...", "Running tests...", "Inspecting result..."},
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

func (m Model) Agent() ghostmodel.Agent { return m.agent }
func (m Model) AddLog(line string) Model {
	if line != "" {
		m.logs = append(m.logs, line)
		if len(m.logs) > 200 {
			m.logs = m.logs[len(m.logs)-200:]
		}
	}
	return m
}

func (m Model) InputFocused() bool { return m.focus == FocusSteering }

func (m Model) Focus() Focus { return m.focus }

func (m Model) InputValue() string { return m.input.Value() }

func (m Model) SetSize(width, height int) Model {
	m.width, m.height = maxInt(width, 0), maxInt(height, 0)
	m.input.SetWidth(maxInt(width-20, 1))
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
		return m, nil
	}

	if isKey && key.Matches(keyMsg, m.keys.Tab) {
		m.focus = FocusSteering
		return m, m.input.Focus()
	}
	return m, nil
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
	logs := strings.Join(m.logs, "\n")
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
