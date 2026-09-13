package agentdetail

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/history"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/chrome"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/layout"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

type Focus int

const (
	FocusSteering Focus = iota
	FocusDecisions
	FocusActivity
)

type BackMsg struct{}

type SteeringSubmittedMsg struct {
	AgentID string
	Text    string
}
type InterruptMsg struct{ AgentID string }
type LogEntry struct{ Type, Text string }

// maxLogEntries bounds each pane's retained history.
const maxLogEntries = 500

// decisionKinds are the entry types routed to the decisions pane. Everything
// else is high-volume activity that would otherwise scroll them out of view.
var decisionKinds = map[string]bool{"response": true, "thought": true, "qac": true, "error": true}

type Model struct {
	theme     theme.Theme
	keys      keymap.KeyMap
	help      help.Model
	agent     ghostmodel.Agent
	logs      []LogEntry
	work      string
	submitKey string

	input     textarea.Model
	decisions viewport.Model
	activity  viewport.Model
	screen    layout.Screen
	follow    bool
	focus     Focus
	width     int
	height    int
}

func New(th theme.Theme, keys keymap.KeyMap, submit ...string) Model {
	in := textarea.New()
	in.Prompt = ""
	in.Placeholder = "steer this agent..."
	in.CharLimit = 64 * 1024
	in.ShowLineNumbers = false
	in.SetHeight(layout.MinSteeringRows)
	in.SetStyles(inputStyles(th))
	h := help.New()
	h.Styles = helpStyles(th)
	m := Model{
		theme: th, keys: keys, help: h, input: in, focus: FocusSteering,
		decisions: viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		activity:  viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		follow:    true,
	}
	if len(submit) > 0 {
		m.submitKey = submit[0]
	} else {
		m.submitKey = "ctrl_enter"
	}
	return m.relayout()
}

func (m Model) SetAgent(agent ghostmodel.Agent) Model {
	m.agent = agent
	m.input.Reset()
	_ = m.input.Focus()
	m.focus = FocusSteering
	return m.relayout()
}

// SyncAgent refreshes the displayed metadata without disturbing focus or the
// steering draft, so status, session, and model stay live while the screen is
// open rather than freezing at the moment it was opened.
func (m Model) SyncAgent(agent ghostmodel.Agent) Model {
	if agent.ID != m.agent.ID {
		return m
	}
	m.agent = agent
	return m
}

// SetWork records the cognition state shown in the sidebar.
func (m Model) SetWork(state string) Model {
	m.work = state
	return m
}

func (m Model) Agent() ghostmodel.Agent { return m.agent }

func (m Model) ClearLogs() Model {
	m.logs = nil
	m.setLogContent()
	return m
}

// SetHistory replaces the projection with one agent's canonical history. The
// detail model renders history; it no longer owns it, so opening a screen shows
// everything the agent did rather than only what arrived while it was open.
func (m Model) SetHistory(entries []history.Entry) Model {
	m.logs = nil
	for _, entry := range entries {
		if entry.Message == "" {
			continue
		}
		m.logs = append(m.logs, projectEntry(entry))
	}
	m.trimLogs()
	m.setLogContent()
	return m.gotoBottom()
}

// AppendEntry projects one newly recorded entry.
func (m Model) AppendEntry(entry history.Entry) Model {
	if entry.Message == "" {
		return m
	}
	m.logs = append(m.logs, projectEntry(entry))
	m.trimLogs()
	m.setLogContent()
	return m.gotoBottom()
}

// ReplaceLastEntry rewrites the most recent row of the same type, which is how
// a streamed response grows and how QAC rewrites a response it has parsed.
func (m Model) ReplaceLastEntry(entry history.Entry) Model {
	projected := projectEntry(entry)
	for i := len(m.logs) - 1; i >= 0; i-- {
		if m.logs[i].Type == projected.Type {
			m.logs[i] = projected
			m.setLogContent()
			return m.gotoBottom()
		}
	}
	return m.AppendEntry(entry)
}

func (m *Model) trimLogs() {
	if len(m.logs) > maxLogEntries {
		m.logs = append([]LogEntry(nil), m.logs[len(m.logs)-maxLogEntries:]...)
	}
}

// projectEntry turns run evidence into the pane's presentation form.
func projectEntry(entry history.Entry) LogEntry {
	return LogEntry{Type: logType(entry.Kind), Text: entry.Message}
}

// logType names the row style for an event kind. The decisions pane is keyed on
// these names, so a kind that is not classified here lands in activity.
func logType(kind event.Kind) string {
	switch kind {
	case event.KindResponse:
		return "response"
	case event.KindThinking:
		return "thought"
	case event.KindCommand:
		return "command"
	case event.KindFile:
		return "file"
	case event.KindTool:
		return "tool"
	case event.KindUsage:
		return "usage"
	case event.KindError:
		return "error"
	case event.KindQAC:
		return "qac"
	case event.KindSession:
		return "session"
	case event.KindSteering:
		return "steering"
	case event.KindStatus:
		return "status"
	default:
		return "event"
	}
}

func (m Model) gotoBottom() Model {
	if m.follow {
		m.decisions.GotoBottom()
		m.activity.GotoBottom()
	}
	return m
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

// relayout rebudgets the two log panes and the steering editor together.
func (m Model) relayout() Model {
	m.screen = layout.Compute(m.width, m.height, m.steeringRows(), paneWeights, m.helpRows())
	if m.screen.TooSmall {
		return m
	}
	m.input.SetWidth(maxInt(m.screen.Content-steerLabelWidth, 1))
	m.input.SetHeight(m.screen.Steering)
	m.decisions.SetWidth(maxInt(m.screen.Content, 1))
	m.decisions.SetHeight(maxInt(m.screen.Panes[0], 1))
	m.activity.SetWidth(maxInt(m.screen.Content, 1))
	m.activity.SetHeight(maxInt(m.screen.Panes[1], 1))
	m.setLogContent()
	return m.gotoBottom()
}

// paneWeights favours the high-volume activity pane while keeping decisions
// large enough to stay readable.
var paneWeights = []float64{0.45, 0.55}

func (m Model) steeringRows() int {
	return layout.SteeringRows(m.input.Value(), maxInt(m.screen.Content-steerLabelWidth, 1))
}

// helpRows measures the expanded footer rather than assuming its height.
func (m Model) helpRows() int {
	if !m.help.ShowAll {
		return 0
	}
	h := m.help
	h.SetWidth(maxInt(m.width-2*chromePad, 1))
	return lipgloss.Height(h.View(m.keys)) + 1
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyPressMsg)
	if isKey {
		// Escape leaves the screen, but must not silently discard a draft.
		if key.Matches(keyMsg, m.keys.Escape) {
			if m.focus == FocusSteering && m.input.Value() != "" {
				m.input.Reset()
				return m.relayout(), nil
			}
			return m, func() tea.Msg { return BackMsg{} }
		}
		if key.Matches(keyMsg, m.keys.Interrupt) && m.agent.ID != "" {
			return m, func() tea.Msg { return InterruptMsg{AgentID: m.agent.ID} }
		}
	}

	if m.focus == FocusSteering {
		if isKey && key.Matches(keyMsg, m.keys.Tab) {
			m.input.Blur()
			m.focus = FocusDecisions
			return m, nil
		}
		if isKey && m.isSubmit(keyMsg) {
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			agentID := m.agent.ID
			m.input.Reset()
			return m.relayout(), func() tea.Msg { return SteeringSubmittedMsg{AgentID: agentID, Text: text} }
		}
		if isKey && keyMsg.String() == "ctrl+c" && m.input.Value() != "" {
			m.input.Reset()
			return m.relayout(), nil
		}
		var cmd tea.Cmd
		before := m.input.Value()
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != before {
			m = m.relayout()
		}
		return m, cmd
	}

	if isKey && key.Matches(keyMsg, m.keys.Help) {
		m.help.ShowAll = !m.help.ShowAll
		return m.relayout(), nil
	}

	if isKey && key.Matches(keyMsg, m.keys.Tab) {
		switch m.focus {
		case FocusDecisions:
			m.focus = FocusActivity
			return m, nil
		default:
			m.focus = FocusSteering
			return m, m.input.Focus()
		}
	}

	var cmd tea.Cmd
	if m.focus == FocusDecisions {
		m.decisions, cmd = m.decisions.Update(msg)
		m.follow = m.decisions.ScrollPercent() >= 0.999
	} else {
		m.activity, cmd = m.activity.Update(msg)
		m.follow = m.activity.ScrollPercent() >= 0.999
	}
	return m, cmd
}

func (m Model) isSubmit(k tea.KeyPressMsg) bool {
	if m.submitKey == "enter" {
		return k.String() == "enter"
	}
	return k.String() == "ctrl+enter"
}

// setLogContent splits the log into the decisions and activity panes.
func (m *Model) setLogContent() {
	width := maxInt(m.screen.Content, 1)
	var decisions, activity []string
	for _, entry := range m.logs {
		line := m.logLine(entry, width)
		if decisionKinds[entry.Type] {
			decisions = append(decisions, line)
		} else {
			activity = append(activity, line)
		}
	}
	muted := style(m.theme, m.theme.Colors.TextMuted)
	if len(decisions) == 0 {
		decisions = []string{muted.Render("No decisions yet.")}
	}
	if len(activity) == 0 {
		activity = []string{muted.Render("No activity yet.")}
	}
	m.decisions.SetContent(strings.Join(decisions, "\n"))
	m.activity.SetContent(strings.Join(activity, "\n"))
}

// logLine renders one entry. Decisions wrap so a full message stays readable;
// activity is clipped to one row to keep the fast-moving pane scannable.
func (m *Model) logLine(entry LogEntry, width int) string {
	const labelWidth = 10
	label := style(m.theme, logColour(m.theme, entry.Type)).Render(chrome.Pad(entry.Type, labelWidth))
	body := maxInt(width-labelWidth-1, 8)
	text := style(m.theme, m.theme.Colors.Text)
	if decisionKinds[entry.Type] {
		wrapped := lipgloss.NewStyle().Width(body).Render(entry.Text)
		lines := strings.Split(wrapped, "\n")
		for i, line := range lines {
			if i == 0 {
				lines[i] = label + " " + text.Render(line)
				continue
			}
			lines[i] = strings.Repeat(" ", labelWidth+1) + text.Render(line)
		}
		return strings.Join(lines, "\n")
	}
	return label + " " + text.Render(chrome.Truncate(collapse(entry.Text), body))
}

func collapse(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")
	return strings.Join(strings.Fields(r.Replace(s)), " ")
}

func logColour(th theme.Theme, kind string) string {
	switch kind {
	case "error":
		return th.Colors.Error
	case "response":
		return th.Colors.AccentHot
	case "thought":
		return th.Colors.Warning
	case "qac":
		return th.Colors.Warning
	case "command", "tool":
		return th.Colors.Accent
	case "file":
		return th.Colors.Success
	default:
		return th.Colors.AccentDim
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

func style(th theme.Theme, colour string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colour))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
