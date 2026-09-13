package dashboard

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/chrome"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/layout"
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

// Work is the cognition state surfaced in the sidebar.
type Work struct {
	Enabled bool
	Owner   string
	State   string
	Goal    string
}

type Model struct {
	theme         theme.Theme
	keys          keymap.KeyMap
	help          help.Model
	agents        []ghostmodel.Agent
	events        []event.Event
	rendered      []string
	renderedWidth int
	work          Work
	submitKey     string

	selected int
	focus    Focus
	input    textarea.Model
	viewport viewport.Model
	screen   layout.Screen
	width    int
	height   int
	follow   bool
}

// SetQAC records cognition state for the sidebar.
func (m Model) SetQAC(enabled bool, owner string) Model {
	m.work.Enabled = enabled
	m.work.Owner = owner
	return m
}

// SetWork records the active work item's state and goal.
func (m Model) SetWork(state, goal string) Model {
	m.work.State = state
	m.work.Goal = goal
	return m
}

func New(th theme.Theme, keys keymap.KeyMap, agents []ghostmodel.Agent, events []event.Event, submit ...string) Model {
	in := textarea.New()
	in.Prompt = ""
	in.Placeholder = "global steering..."
	in.CharLimit = 64 * 1024
	in.ShowLineNumbers = false
	in.SetHeight(layout.MinSteeringRows)
	in.SetStyles(inputStyles(th))
	h := help.New()
	h.Styles = helpStyles(th)

	m := Model{
		theme: th, keys: keys, help: h, agents: append([]ghostmodel.Agent(nil), agents...),
		events: append([]event.Event(nil), events...), focus: FocusAgents, input: in,
		viewport: viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)), follow: true,
	}
	if len(submit) > 0 {
		m.submitKey = submit[0]
	} else {
		m.submitKey = "ctrl_enter"
	}
	return m.relayout()
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) SelectedIndex() int { return m.selected }

func (m Model) AgentAt(index int) (ghostmodel.Agent, bool) {
	if index < 0 || index >= len(m.agents) {
		return ghostmodel.Agent{}, false
	}
	return m.agents[index], true
}

func (m Model) InputFocused() bool { return m.focus == FocusSteering }

func (m Model) Focus() Focus { return m.focus }

func (m Model) InputValue() string { return m.input.Value() }

// Screen exposes the resolved geometry for tests.
func (m Model) Screen() layout.Screen { return m.screen }

func (m Model) SetSize(width, height int) Model {
	m.width, m.height = maxInt(width, 0), maxInt(height, 0)
	return m.relayout()
}

// relayout recomputes every pane from one shared row budget. It runs on resize,
// on help toggle, and after any input change that alters the steering height.
func (m Model) relayout() Model {
	m.screen = layout.Compute(m.width, m.height, m.steeringRows(), []float64{1}, m.helpRows())
	if m.screen.TooSmall {
		return m
	}
	m.input.SetWidth(maxInt(m.screen.Content-steerLabelWidth, 1))
	m.input.SetHeight(m.screen.Steering)
	m.viewport.SetWidth(maxInt(m.screen.Content, 1))
	m.viewport.SetHeight(maxInt(m.screen.Panes[0], 1))
	if m.renderedWidth != m.screen.Content {
		m.setEventContent()
	}
	if m.follow {
		m.viewport.GotoBottom()
	}
	return m
}

func (m Model) steeringRows() int {
	return layout.SteeringRows(m.input.Value(), maxInt(m.screen.Content-steerLabelWidth, 1))
}

// helpRows measures the expanded footer rather than assuming its height, so a
// changed key map can never push the layout out of the frame.
func (m Model) helpRows() int {
	if !m.help.ShowAll {
		return 0
	}
	h := m.help
	h.SetWidth(maxInt(m.width-2*chromePad, 1))
	return lipgloss.Height(h.View(m.keys)) + 1
}

func (m Model) SetEvents(events []event.Event) Model {
	m.events = append([]event.Event(nil), events...)
	m.setEventContent()
	if m.follow {
		m.viewport.GotoBottom()
	}
	return m
}

// maxRetainedEvents bounds the cached rows in step with the caller's own cap.
const maxRetainedEvents = 2000

// AppendEvent adds one rendered row; replacement and resize paths rebuild so
// cached rows cannot outlive their events or the width that shaped them.
func (m Model) AppendEvent(item event.Event) Model {
	m.events = append(m.events, item)
	if len(m.events) > maxRetainedEvents {
		m.events = append([]event.Event(nil), m.events[len(m.events)-maxRetainedEvents:]...)
	}
	// The first real event replaces the placeholder rather than following it.
	if len(m.events) == 1 {
		m.rendered = nil
	}
	m.rendered = append(m.rendered, m.eventLine(item, maxInt(m.screen.Content, 1)))
	if drop := len(m.rendered) - len(m.events); drop > 0 {
		m.rendered = append([]string(nil), m.rendered[drop:]...)
	}
	m.viewport.SetContent(strings.Join(m.rendered, "\n"))
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
			break
		}
	}
	return m
}

// UpdateAgentRuntime sets an agent's elapsed-time column.
func (m Model) UpdateAgentRuntime(id, elapsed string) Model {
	for i := range m.agents {
		if m.agents[i].ID == id {
			m.agents[i].Runtime = elapsed
			break
		}
	}
	return m
}

// Agent returns the current state of one agent, so the detail screen can stay
// in step with the roster instead of holding a copy taken when it was opened.
func (m Model) Agent(id string) (ghostmodel.Agent, bool) {
	for _, a := range m.agents {
		if a.ID == id {
			return a, true
		}
	}
	return ghostmodel.Agent{}, false
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
		if isKey && m.isSubmit(keyMsg) {
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.Reset()
			return m.relayout(), func() tea.Msg { return SteeringSubmittedMsg{Text: text} }
		}
		if isKey && keyMsg.String() == "ctrl+c" && m.input.Value() != "" {
			m.input.Reset()
			return m.relayout(), nil
		}
		var cmd tea.Cmd
		before := m.input.Value()
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != before {
			// The editor grows and shrinks with its content, so the panes above
			// it have to be rebudgeted on every change.
			m = m.relayout()
		}
		return m, cmd
	}

	if isKey && key.Matches(keyMsg, m.keys.Help) {
		m.help.ShowAll = !m.help.ShowAll
		return m.relayout(), nil
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

func (m Model) isSubmit(k tea.KeyPressMsg) bool {
	if m.submitKey == "enter" {
		return k.String() == "enter"
	}
	return k.String() == "ctrl+enter"
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
	width := maxInt(m.screen.Content, 1)
	lines := make([]string, 0, len(m.events))
	for _, item := range m.events {
		lines = append(lines, m.eventLine(item, width))
	}
	if len(lines) == 0 {
		lines = []string{style(m.theme, m.theme.Colors.TextMuted).Render("No events yet.")}
	}
	m.rendered = lines
	m.renderedWidth = width
	m.viewport.SetContent(strings.Join(lines, "\n"))
}

// eventLine renders one stream row, clipped to the pane so a long summary can
// never reflow the columns beneath it.
func (m *Model) eventLine(item event.Event, width int) string {
	stamp := style(m.theme, m.theme.Colors.TextMuted).Render(item.Time.Format("15:04:05"))
	source := style(m.theme, m.theme.Colors.Accent).Bold(true).Render(chrome.Pad(item.Source, 8))
	kind := style(m.theme, kindColour(m.theme, item.Kind)).Render(chrome.Pad(string(item.Kind), 9))
	const prefix = 8 + 2 + 8 + 2 + 9 + 2
	message := chrome.Truncate(collapse(item.Message), maxInt(width-prefix, 8))
	return strings.Join([]string{stamp, source, kind, style(m.theme, m.theme.Colors.Text).Render(message)}, "  ")
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
	case event.KindResponse:
		return th.Colors.AccentHot
	case event.KindQAC:
		return th.Colors.Warning
	case event.KindSteering:
		return th.Colors.Accent
	case event.KindCommand, event.KindTool:
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

func inputStyles(th theme.Theme) textarea.Styles {
	styles := textarea.DefaultDarkStyles()
	text := style(th, th.Colors.Text)
	muted := style(th, th.Colors.TextMuted)
	for _, s := range []*textarea.StyleState{&styles.Focused, &styles.Blurred} {
		s.Text = text
		s.Placeholder = muted
		s.Prompt = muted
		s.CursorLine = lipgloss.NewStyle()
		s.CursorLineNumber = muted
		s.LineNumber = muted
		s.EndOfBuffer = muted
		s.Base = lipgloss.NewStyle()
	}
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

func style(th theme.Theme, colour string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colour))
}
