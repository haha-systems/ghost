package dashboard

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/chrome"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/layout"
	"github.com/haha-systems/ghost/internal/ui/pane"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

type Focus int

const (
	FocusAgents Focus = iota
	FocusSteering
	FocusEvents
)

type OpenAgentMsg struct{ Index int }
type OpenEpistemicMsg struct{}

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
	operator      *epistemic.OperatorView
	supplement    epistemic.OperatorSupplement
	cognition     string
	submitKey     string

	selected int
	focus    Focus
	input    textarea.Model
	stream   pane.Pane
	screen   layout.Screen
	width    int
	height   int
}

// SetQAC records cognition state for the sidebar.
func (m Model) SetQAC(enabled bool, owner string) Model {
	m.work.Enabled = enabled
	m.work.Owner = owner
	if m.operator != nil {
		m.cognition = owner
	}
	return m
}

// SetWork records the active work item's state and goal.
func (m Model) SetWork(state, goal string) Model {
	m.work.State = state
	m.work.Goal = goal
	return m
}

// SetOperatorView records CES-owned work state and the separately selected
// cognitive resource. It does not change focus, input, or event scrolling.
func (m Model) SetOperatorView(view *epistemic.OperatorView, cognition string) Model {
	if view == nil {
		m.operator = nil
	} else {
		copy := *view
		if view.LeadingHypothesis != nil {
			hypothesis := *view.LeadingHypothesis
			copy.LeadingHypothesis = &hypothesis
		}
		if view.ActiveFrame != nil {
			frame := *view.ActiveFrame
			copy.ActiveFrame = &frame
		}
		if view.LastTransition != nil {
			transition := *view.LastTransition
			copy.LastTransition = &transition
		}
		m.operator = &copy
	}
	m.cognition = cognition
	return m
}

// SetOperatorSupplement applies UX details that are outside CES OperatorView.
func (m Model) SetOperatorSupplement(supplement epistemic.OperatorSupplement) Model {
	m.supplement = supplement
	m.supplement.InvalidatedPhases = append([]epistemic.Phase(nil), supplement.InvalidatedPhases...)
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
		events: cloneEvents(events), focus: FocusAgents, input: in,
		stream: pane.New(),
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
	m.stream.SetSize(m.screen.Content, m.screen.Panes[0])
	if m.renderedWidth != m.screen.Content {
		m.setEventContent()
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
	m.events = cloneEvents(events)
	m.setEventContent()
	return m
}

// maxRetainedEvents bounds the cached rows in step with the caller's own cap.
const maxRetainedEvents = 2000

// AppendEvent adds one rendered row; replacement and resize paths rebuild so
// cached rows cannot outlive their events or the width that shaped them.
func (m Model) AppendEvent(item event.Event) Model {
	item = cloneEvent(item)
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
	m.stream.SetContent(strings.Join(m.rendered, "\n"))
	m.stream.Noted(1)
	return m
}

// ReplaceLastEvent rewrites the most recent row in place. A streamed response
// grows one fragment at a time, so re-rendering the whole stream for each would
// cost the run O(n) renders per fragment.
func (m Model) ReplaceLastEvent(item event.Event) Model {
	item = cloneEvent(item)
	if len(m.events) == 0 {
		return m.AppendEvent(item)
	}
	m.events[len(m.events)-1] = item
	if len(m.rendered) == 0 {
		m.setEventContent()
		return m
	}
	m.rendered[len(m.rendered)-1] = m.eventLine(item, maxInt(m.screen.Content, 1))
	// A rewritten row is the same entry growing, not a new arrival.
	m.stream.SetContent(strings.Join(m.rendered, "\n"))
	return m
}

func cloneEvents(events []event.Event) []event.Event {
	out := make([]event.Event, len(events))
	for i := range events {
		out[i] = cloneEvent(events[i])
	}
	return out
}

func cloneEvent(item event.Event) event.Event {
	if item.Metadata != nil {
		metadata := make(map[string]string, len(item.Metadata))
		for key, value := range item.Metadata {
			metadata[key] = value
		}
		item.Metadata = metadata
	}
	if item.Raw != nil {
		item.Raw = append([]byte(nil), item.Raw...)
	}
	return item
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
	// The pointer, not the keyboard focus, decides what the wheel scrolls.
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		return m.scrollAtPointer(wheel)
	}
	keyMsg, isKey := msg.(tea.KeyPressMsg)
	if isKey {
		if m.focus != FocusSteering && key.Matches(keyMsg, m.keys.OpenEpistemic) {
			return m, func() tea.Msg { return OpenEpistemicMsg{} }
		}
		if key.Matches(keyMsg, m.keys.Escape) {
			m.focus = FocusAgents
			m.input.Blur()
			return m, nil
		}
	}

	if m.focus == FocusSteering {
		if isKey && key.Matches(keyMsg, m.keys.Tab) {
			m.input.Blur()
			m.focus = FocusAgents
			return m, nil
		}
		if isKey && key.Matches(keyMsg, m.keys.ShiftTab) {
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
			m.focus = FocusSteering
			return m, m.input.Focus()
		}
		if isKey && key.Matches(keyMsg, m.keys.ShiftTab) {
			m.focus = FocusAgents
			return m, nil
		}
		if isKey {
			pane.HandleKey(&m.stream, m.keys, keyMsg)
			return m, nil
		}
		return m, m.stream.Update(msg)
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
			m.focus = FocusEvents
			return m, nil
		case key.Matches(keyMsg, m.keys.ShiftTab):
			m.focus = FocusSteering
			return m, m.input.Focus()
		}
	}
	return m, nil
}

// scrollAtPointer routes a wheel event to whatever the pointer is over,
// leaving keyboard focus where it was.
func (m Model) scrollAtPointer(wheel tea.MouseWheelMsg) (Model, tea.Cmd) {
	delta := pane.WheelDelta(wheel)
	if delta == 0 || m.screen.TooSmall {
		return m, nil
	}
	if bounds := m.screen.PaneBounds(); len(bounds) == 1 && bounds[0].Contains(wheel.X, wheel.Y) {
		m.stream.Scroll(delta)
		return m, nil
	}
	if m.screen.SteeringBounds().Contains(wheel.X, wheel.Y) {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(wheel)
		return m, cmd
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
	m.stream.SetContent(strings.Join(lines, "\n"))
}

// eventLine renders one stream row, clipped to the pane so a long summary can
// never reflow the columns beneath it.
func (m *Model) eventLine(item event.Event, width int) string {
	stamp := style(m.theme, m.theme.Colors.TextMuted).Render(item.Time.Format("15:04:05"))
	if semanticKind(item.Kind) {
		kindText := strings.ToUpper(string(item.Kind))
		sourceWidth := maxInt(len([]rune(item.Source)), 3)
		kindWidth := len([]rune(kindText))
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
