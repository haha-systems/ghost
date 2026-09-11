package dashboard

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

type Focus int

const (
	FocusAgents Focus = iota
	FocusSteering
	FocusEvents
)

type OpenAgentMsg struct{ Index int }

type SteeringSubmittedMsg struct{ Text string }

type Model struct {
	theme      theme.Theme
	keys       keymap.KeyMap
	help       help.Model
	agents     []ghostmodel.Agent
	events     []event.Event
	qacEnabled bool
	qacOwner   string
	submitKey  string

	selected int
	focus    Focus
	input    textarea.Model
	viewport viewport.Model
	width    int
	height   int
	follow   bool
}

func (m Model) SetQAC(enabled bool, owner string) Model {
	m.qacEnabled = enabled
	m.qacOwner = owner
	return m
}

func New(th theme.Theme, keys keymap.KeyMap, agents []ghostmodel.Agent, events []event.Event, submit ...string) Model {
	in := textarea.New()
	in.Prompt = ""
	in.Placeholder = "global steering..."
	in.CharLimit = 64 * 1024
	in.SetHeight(1)
	h := help.New()
	h.Styles = helpStyles(th)

	m := Model{
		theme: th, keys: keys, help: h, agents: append([]ghostmodel.Agent(nil), agents...),
		events: append([]event.Event(nil), events...), focus: FocusAgents, input: in,
		viewport: viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)), follow: true,
	}
	m.setEventContent()
	if len(submit) > 0 {
		m.submitKey = submit[0]
	} else {
		m.submitKey = "ctrl_enter"
	}
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) SelectedIndex() int { return m.selected }

func (m Model) SelectedAgent() (ghostmodel.Agent, bool) {
	if m.selected < 0 || m.selected >= len(m.agents) {
		return ghostmodel.Agent{}, false
	}
	return m.agents[m.selected], true
}

func (m Model) AgentAt(index int) (ghostmodel.Agent, bool) {
	if index < 0 || index >= len(m.agents) {
		return ghostmodel.Agent{}, false
	}
	return m.agents[index], true
}

func (m Model) InputFocused() bool { return m.focus == FocusSteering }

func (m Model) Focus() Focus { return m.focus }

func (m Model) InputValue() string { return m.input.Value() }

func (m Model) SetSize(width, height int) Model {
	m.width, m.height = maxInt(width, 0), maxInt(height, 0)
	m.input.SetWidth(maxInt(width-18, 1))
	m.input.SetHeight(1)
	wasFocused := m.input.Focused()
	if !wasFocused {
		_ = m.input.Focus()
	}
	m.input, _ = m.input.Update(tea.WindowSizeMsg{Width: width, Height: height})
	if !wasFocused {
		m.input.Blur()
	}
	m.viewport.SetWidth(maxInt(width-4, 1))
	m.updateViewportHeight()
	m.setEventContent()
	if m.follow {
		m.viewport.GotoBottom()
	}
	return m
}

func (m Model) SetEvents(events []event.Event) Model {
	m.events = append([]event.Event(nil), events...)
	m.setEventContent()
	if m.follow {
		m.viewport.GotoBottom()
	}
	return m
}

func (m Model) SetAgents(agents []ghostmodel.Agent) Model {
	m.agents = append([]ghostmodel.Agent(nil), agents...)
	return m
}
func (m Model) UpdateAgent(id string, state ghostmodel.AgentState, activity, sessionID string) Model {
	for i := range m.agents {
		if m.agents[i].ID == id {
			m.agents[i].State = state
			m.agents[i].Activity = activity
			if sessionID != "" {
				m.agents[i].SessionID = sessionID
			}
			break
		}
	}
	return m
}
func (m Model) UpdateAgentMetadata(id, model, sessionID string) Model {
	for i := range m.agents {
		if m.agents[i].ID == id {
			if model != "" {
				m.agents[i].Model = model
			}
			if sessionID != "" {
				m.agents[i].SessionID = sessionID
			}
			if m.agents[i].Runtime == "—" {
				m.agents[i].Runtime = "00m 00s"
			}
			break
		}
	}
	return m
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyPressMsg)
	if isKey {
		if key.Matches(keyMsg, m.keys.Escape) {
			m.focus = FocusAgents
			m.input.Blur()
			return m, nil
		}
	}

	if m.focus == FocusSteering {
		if isKey && key.Matches(keyMsg, m.keys.Tab) {
			m.input.Blur()
			m.focus = FocusEvents
			return m, nil
		}
		if isKey && ((m.submitKey == "enter" && keyMsg.Text == "enter") || (m.submitKey == "ctrl_enter" && keyMsg.Text == "ctrl+enter")) {
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.Reset()
			return m, func() tea.Msg { return SteeringSubmittedMsg{Text: text} }
		}
		if isKey && keyMsg.Text == "ctrl+c" && m.input.Value() != "" {
			m.input.Reset()
			return m, nil
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

	if m.focus == FocusEvents {
		if isKey && key.Matches(keyMsg, m.keys.Tab) {
			m.focus = FocusAgents
			return m, nil
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.follow = m.viewport.ScrollPercent() >= 0.999
		return m, cmd
	}

	if isKey {
		switch {
		case key.Matches(keyMsg, m.keys.Up):
			m.selectPrevious()
		case key.Matches(keyMsg, m.keys.Down):
			m.selectNext()
		case key.Matches(keyMsg, m.keys.Enter):
			return m, func() tea.Msg { return OpenAgentMsg{Index: m.selected} }
		case key.Matches(keyMsg, m.keys.Tab):
			m.focus = FocusSteering
			return m, m.input.Focus()
		}
	}
	return m, nil
}

func (m *Model) selectPrevious() {
	if len(m.agents) == 0 {
		return
	}
	m.selected = (m.selected - 1 + len(m.agents)) % len(m.agents)
}

func (m *Model) selectNext() {
	if len(m.agents) == 0 {
		return
	}
	m.selected = (m.selected + 1) % len(m.agents)
}

func (m *Model) setEventContent() {
	lines := make([]string, 0, len(m.events))
	for _, item := range m.events {
		stamp := item.Time.Format("15:04:05")
		stampText := style(m.theme, m.theme.Colors.TextMuted).Render(stamp)
		sourceText := style(m.theme, m.theme.Colors.Accent).Bold(true).Render(fmt.Sprintf("%-8s", item.Source))
		kindText := style(m.theme, m.theme.Colors.AccentDim).Render(fmt.Sprintf("%-8s", item.Kind))
		messageText := style(m.theme, m.theme.Colors.Text).Render(item.Message)
		lines = append(lines, strings.Join([]string{stampText, sourceText, kindText, messageText}, "  "))
	}
	if len(lines) == 0 {
		lines = []string{"No events yet."}
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func style(th theme.Theme, color string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
