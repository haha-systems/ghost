package app

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/history"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/ui/agentdetail"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
	uiEpistemic "github.com/haha-systems/ghost/internal/ui/epistemic"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.ColorProfileMsg:
		m.colorProfile = msg.Profile
		return m, nil
	case tickMsg:
		return m.handleTick(msg)
	case sessionsStartedMsg:
		return m.handleSessionsStarted(msg)
	case sessionEventMsg:
		return m.handleSessionEvent(msg.event)
	case runEventMsg:
		return m.handleRunEvent(msg.event)
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case dashboard.OpenAgentMsg:
		return m.handleNavigation(msg)
	case dashboard.OpenEpistemicMsg:
		return m.handleNavigation(msg)
	case uiEpistemic.BackMsg:
		return m.handleNavigation(msg)
	case dashboard.SteeringSubmittedMsg:
		return m.handleGlobalSteering(msg)
	case agentdetail.SteeringSubmittedMsg:
		return m.handleAgentSteering(msg)
	case agentdetail.BackMsg:
		return m.handleNavigation(msg)
	case agentdetail.InterruptMsg:
		return m.handleAgentInterrupt(msg)
	case sessionErrorMsg:
		return m.handleSessionError(msg)
	}

	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		if m.screen == LegacyDashboardScreen && key.Matches(keyMsg, m.keys.Escape) {
			m.screen = m.returnScreen
			return m, nil
		}
		if m.screen == ConversationScreen && key.Matches(keyMsg, m.keys.ForceQuit) {
			return m, m.quitCmd()
		}
		if key.Matches(keyMsg, m.keys.ForceQuit) && (!m.InputFocused() || !m.HasSteeringText()) {
			return m, m.quitCmd()
		}
		if key.Matches(keyMsg, m.keys.Quit) && !m.InputFocused() && m.screen != ConversationScreen {
			return m, m.quitCmd()
		}
	}

	return m.updateScreen(msg)
}

func (m Model) handleNavigation(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dashboard.OpenAgentMsg:
		if agent, ok := m.dashboard.AgentAt(msg.Index); ok {
			m.detail = m.detail.SetAgent(agent).SetHistory(m.history.Agent(agent.ID)).SetSize(m.width, m.height)
			m.returnScreen = m.screen
			m.screen = AgentDetailScreen
		}
	case dashboard.OpenEpistemicMsg:
		m.returnScreen = m.screen
		m.screen = EpistemicScreen
	case uiEpistemic.BackMsg, agentdetail.BackMsg:
		m.screen = m.returnScreen
	}
	return m, nil
}

func (m Model) handleAgentSteering(msg agentdetail.SteeringSubmittedMsg) (tea.Model, tea.Cmd) {
	m.record(msg.AgentID, event.Event{Source: strings.ToUpper(msg.AgentID), Kind: event.KindSteering, Message: "steering updated: " + msg.Text}, nil)
	s := m.sessions[msg.AgentID]
	if s == nil {
		return m, nil
	}
	input := runtime.Input{Text: msg.Text}
	return m, func() tea.Msg {
		if s.State() == runtime.StateRunning {
			return sessionErrorMsg{agentID: msg.AgentID, err: s.Steer(context.Background(), input)}
		}
		return sessionErrorMsg{agentID: msg.AgentID, err: s.Send(context.Background(), input)}
	}
}

func (m Model) handleAgentInterrupt(msg agentdetail.InterruptMsg) (tea.Model, tea.Cmd) {
	s := m.sessions[msg.AgentID]
	if s == nil {
		return m, nil
	}
	return m, func() tea.Msg { return sessionErrorMsg{agentID: msg.AgentID, err: s.Interrupt(context.Background())} }
}

func (m Model) handleSessionError(msg sessionErrorMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.record(msg.agentID, event.Event{Source: strings.ToUpper(msg.agentID), Kind: event.KindError, Message: msg.err.Error()}, nil)
	}
	return m, nil
}

func (m Model) updateScreen(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case ConversationScreen:
		return m.updateConversation(msg)
	case LegacyDashboardScreen, DashboardScreen:
		m.dashboard, cmd = m.dashboard.Update(msg)
	case AgentDetailScreen:
		m.detail, cmd = m.detail.Update(msg)
	case EpistemicScreen:
		m.epistemic, cmd = m.epistemic.Update(msg)
	}
	return m, cmd
}

func (m Model) handleTick(msg tickMsg) (tea.Model, tea.Cmd) {
	now := time.Time(msg)
	for id, started := range m.started {
		m.dashboard = m.dashboard.UpdateAgentRuntime(id, formatElapsed(now.Sub(started)))
	}
	m.syncDetail()
	return m, tickCmd()
}

func (m Model) handleSessionsStarted(msg sessionsStartedMsg) (tea.Model, tea.Cmd) {
	m.sessions = msg.sessions
	_ = m.controller.SetSessions(msg.sessions)
	m.refreshProjection()
	for id, err := range msg.errors {
		m.record(id, event.Event{Source: strings.ToUpper(id), Kind: event.KindError, Message: err.Error()}, nil)
	}
	for id, s := range msg.sessions {
		meta := s.Metadata()
		m.dashboard = m.dashboard.UpdateAgentMetadata(id, meta.Model, meta.ThreadID)
		started := s.Stats().StartedAt
		if started.IsZero() {
			started = timeNow()
		}
		m.started[id] = started
		m.dashboard = m.dashboard.UpdateAgentRuntime(id, formatElapsed(0))
	}
	m.syncDetail()
	cmds := make([]tea.Cmd, 0, len(msg.sessions))
	for _, s := range msg.sessions {
		cmds = append(cmds, waitSessionEvent(s))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleSessionEvent(e runtime.Event) (tea.Model, tea.Cmd) {
	m.record(e.AgentID, event.Event{Time: e.Time, Source: strings.ToUpper(e.AgentID), Kind: dashboardEventKind(e.Kind), Message: e.Summary, SessionID: e.SessionID, TurnID: e.TurnID, Raw: e.Raw}, nil)
	if s := m.sessions[e.AgentID]; s != nil {
		state := ghostmodel.AgentIdle
		switch s.State() {
		case runtime.StateRunning:
			state = ghostmodel.AgentActive
		case runtime.StateInterrupting:
			state = ghostmodel.AgentWaiting
		case runtime.StateFailed:
			state = ghostmodel.AgentError
		}
		m.dashboard = m.dashboard.UpdateAgent(e.AgentID, state, e.Summary, e.SessionID)
		m.syncDetail()
	}
	_ = m.controller.Observe(e)
	if s := m.sessions[e.AgentID]; s != nil {
		return m, waitSessionEvent(s)
	}
	return m, nil
}

func (m Model) handleGlobalSteering(msg dashboard.SteeringSubmittedMsg) (tea.Model, tea.Cmd) {
	_ = m.controller.Steer(msg.Text)
	return m, nil
}

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	m.dashboard = m.dashboard.SetSize(msg.Width, msg.Height)
	m.detail = m.detail.SetSize(msg.Width, msg.Height)
	m.epistemic = m.epistemic.SetSize(msg.Width, msg.Height)
	if !m.ticking {
		m.ticking = true
		return m, tickCmd()
	}
	return m, nil
}

// syncDetail keeps the open detail screen's metadata in step with the roster.
// Without it, status, session, model, and runtime freeze at the moment the
// screen was opened.
func (m *Model) syncDetail() {
	id := m.detail.Agent().ID
	if id == "" {
		return
	}
	if agent, ok := m.dashboard.Agent(id); ok {
		m.detail = m.detail.SyncAgent(agent)
	}
}

// syncWork publishes cognition state to both screens.
func (m *Model) syncWork() {
	if !m.runStatus.Enabled {
		return
	}
	work := m.runStatus.Work
	if work == nil {
		m.dashboard = m.dashboard.SetWork("", "")
		m.detail = m.detail.SetWork("")
		return
	}
	state := string(work.State)
	if m.runStatus.State.Task.ID != "" {
		state = string(m.runStatus.State.Task.Status)
	}
	m.dashboard = m.dashboard.SetQAC(true, work.OwnerResource).SetWork(state, work.Goal)
	m.detail = m.detail.SetWork(state)
}
func (m Model) OperatorView() (epistemic.OperatorView, bool) {
	return m.runStatus.View, m.runStatus.State.Task.ID != ""
}

func (m *Model) replaceLatestResponse(agent, text string) {
	entry, ok := m.history.ReplaceLatest(agent, event.KindResponse, text)
	if !ok {
		return
	}
	for i := len(m.events) - 1; i >= 0; i-- {
		if m.events[i].Source == strings.ToUpper(agent) && m.events[i].Kind == event.KindResponse {
			m.events[i].Message = text
			m.dashboard = m.dashboard.SetEvents(m.events)
			break
		}
	}
	m.projectDetail(entry, true)
}

// dashboardEventKind maps a runtime kind onto its presentation kind 1:1, so the
// backend's normalization survives all the way to the event stream.
func dashboardEventKind(kind runtime.EventKind) event.Kind {
	switch kind {
	case runtime.KindMessage:
		return event.KindResponse
	case runtime.KindError:
		return event.KindError
	case runtime.KindStatus:
		return event.KindStatus
	case runtime.KindSession:
		return event.KindSession
	case runtime.KindThinking:
		return event.KindThinking
	case runtime.KindCommand:
		return event.KindCommand
	case runtime.KindFile:
		return event.KindFile
	case runtime.KindTool:
		return event.KindTool
	case runtime.KindUsage:
		return event.KindUsage
	default:
		return event.KindAgent
	}
}

type sessionErrorMsg struct {
	agentID string
	err     error
}

func (m Model) quitCmd() tea.Cmd {
	return func() tea.Msg {
		if err := m.controller.Close(); err != nil && m.logger != nil {
			m.logger.Warn("close run", "error", err)
		}
		return tea.QuitMsg{}
	}
}

// maxEvents bounds the dashboard's projection of the stream. Canonical history
// carries its own, larger bound; this one keeps the rendered rows cheap.
const maxEvents = 2000

// appendEvent records an event that belongs to no single agent, such as a
// system notice or a coordinator error.
func (m *Model) appendEvent(item event.Event) { m.record("", item, nil) }

// record projects an operator event and asks the controller to trace it.
// The controller is the only trace writer. History here is the screen projection.
func (m *Model) record(agentID string, item event.Event, meta map[string]string) {
	m.recordInternal(agentID, item, meta, true)
}

// recordProjection updates canonical history and the live views. The controller
// already wrote the authoritative event, so this does not trace it again.
func (m *Model) recordProjection(agentID string, item event.Event, meta map[string]string) {
	m.recordInternal(agentID, item, meta, false)
}

func (m *Model) recordInternal(agentID string, item event.Event, meta map[string]string, writeTrace bool) {
	if item.Time.IsZero() {
		item.Time = timeNow()
	}
	if item.Kind == event.KindError && m.logger != nil {
		m.logger.Error(item.Message, "source", item.Source, "agent_id", agentID)
	}
	entry := historyEntry(agentID, item, meta)
	if writeTrace {
		_ = m.controller.Trace(agentID, item, meta)
	}
	stored, merged := m.history.Append(entry)
	// The dashboard mirrors the store's coalescing decision rather than
	// repeating it, so the two projections cannot disagree about how many rows
	// a streamed response occupies.
	if merged {
		item.Message = stored.Message
		item.Raw = stored.Raw
		if len(m.events) > 0 {
			m.events[len(m.events)-1] = item
			m.dashboard = m.dashboard.ReplaceLastEvent(item)
		}
	} else {
		m.events = append(m.events, item)
		if len(m.events) > maxEvents {
			m.events = append([]event.Event(nil), m.events[len(m.events)-maxEvents:]...)
		}
		m.dashboard = m.dashboard.AppendEvent(item)
	}
	m.projectDetail(stored, merged)
}

// projectDetail forwards an entry to the open detail screen when it belongs to
// the agent on display.
func (m *Model) projectDetail(entry history.Entry, merged bool) {
	if !history.Belongs(entry, m.detail.Agent().ID) {
		return
	}
	if merged {
		m.detail = m.detail.ReplaceLastEntry(entry)
		return
	}
	m.detail = m.detail.AppendEntry(entry)
}

// historyEntry lifts a presentation event into run evidence. Source is display
// text; agentID is the identity history is keyed on.
func historyEntry(agentID string, item event.Event, meta map[string]string) history.Entry {
	metadata := make(map[string]string, len(item.Metadata)+len(meta))
	for key, value := range item.Metadata {
		metadata[key] = value
	}
	for key, value := range meta {
		metadata[key] = value
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	return history.Entry{
		Time:      item.Time,
		AgentID:   agentID,
		SessionID: item.SessionID,
		TurnID:    item.TurnID,
		Kind:      item.Kind,
		Message:   item.Message,
		Metadata:  metadata,
		Raw:       item.Raw,
	}
}

var timeNow = func() time.Time { return time.Now() }
