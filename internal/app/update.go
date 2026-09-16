package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/cognition"
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
	case tickMsg:
		now := time.Time(msg)
		for id, started := range m.started {
			m.dashboard = m.dashboard.UpdateAgentRuntime(id, formatElapsed(now.Sub(started)))
		}
		m.syncDetail()
		return m, tickCmd()
	case sessionsStartedMsg:
		m.sessions = msg.sessions
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
	case sessionEventMsg:
		e := msg.event
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
		if m.cognition != nil {
			if plan, err := m.cognition.Observe(context.Background(), e, m.sessions, time.Now()); err != nil {
				m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
			} else if plan != nil && m.cesActive() {
				// CES owns phase progression. A QAC request observed on a
				// persistent session cannot re-enter the monolithic path.
				m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: "ignored qac request outside the CES phase path"})
			} else if plan != nil {
				m.replaceLatestResponse(e.AgentID, plan.Visible)
				cmds := []tea.Cmd{m.dispatchCognition(*plan)}
				if s := m.sessions[e.AgentID]; s != nil {
					cmds = append(cmds, waitSessionEvent(s))
				}
				return m, tea.Batch(cmds...)
			}
		}
		if s := m.sessions[e.AgentID]; s != nil {
			return m, waitSessionEvent(s)
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.dashboard = m.dashboard.SetSize(msg.Width, msg.Height)
		m.detail = m.detail.SetSize(msg.Width, msg.Height)
		m.epistemic = m.epistemic.SetSize(msg.Width, msg.Height)
		// The elapsed-time clock starts with the first size, which is also the
		// first point at which anything can be drawn. The guard keeps a resize
		// from starting a second ticker.
		if !m.ticking {
			m.ticking = true
			return m, tickCmd()
		}
		return m, nil
	case dashboard.OpenAgentMsg:
		if agent, ok := m.dashboard.AgentAt(msg.Index); ok {
			m.detail = m.detail.SetAgent(agent).
				SetHistory(m.history.Agent(agent.ID)).
				SetSize(m.width, m.height)
			m.screen = AgentDetailScreen
		}
		return m, nil
	case dashboard.OpenEpistemicMsg:
		m.screen = EpistemicScreen
		return m, nil
	case uiEpistemic.BackMsg:
		m.screen = DashboardScreen
		return m, nil
	case dashboard.SteeringSubmittedMsg:
		m.appendEvent(event.Event{Source: "SYSTEM", Kind: event.KindSteering, Message: "global steering updated: " + msg.Text})
		if m.cognition != nil && m.cognition.Work() == nil {
			// CES controls the run from here: the task is never sent whole to
			// a persistent session.
			return m, m.startCESWork(msg.Text)
		}
		if m.cesActive() {
			// Steering during CES work is operator evidence, not a task
			// dispatch. The phase in flight owns the cognitive session.
			m.appendEvent(event.Event{Source: "CES", Kind: event.KindSteering, Message: "steering recorded during " + strings.ToUpper(string(m.cesStore.State().Task.Phase))})
			return m, nil
		}
		if m.cognition != nil && m.cognition.Work() != nil {
			work := m.cognition.Work()
			s := m.sessions[work.OwnerAgent]
			if s == nil {
				return m, nil
			}
			m.cognition.ExpectTurn(work.OwnerAgent)
			input := runtime.Input{Text: msg.Text}
			return m, func() tea.Msg {
				if s.State() == runtime.StateRunning {
					return sessionErrorMsg{agentID: work.OwnerAgent, err: s.Steer(context.Background(), input)}
				}
				return sessionErrorMsg{agentID: work.OwnerAgent, err: s.Send(context.Background(), input)}
			}
		}
		return m, nil
	case cesPhaseResultMsg:
		return m.handlePhaseResult(msg)
	case cognitionResultMsg:
		if msg.err != nil {
			m.cognition.Fail(msg.plan)
			m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: msg.err.Error()})
			return m, nil
		}
		if err := m.cognition.Commit(msg.plan, time.Now()); err != nil {
			m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
		}
		if !msg.plan.Initial && msg.plan.Action == "stop" {
			if m.orchestrator != nil && m.cesStore != nil {
				if err := m.orchestrator.ResourceUnavailable(); err == nil {
					state := m.cesStore.State().Task
					m.cognition.SyncWorkStatus(cognition.WorkIncomplete, state.TerminalReason, time.Now())
					m.record("", event.Event{Source: "CES", Kind: event.KindCES, Message: "work incomplete: " + state.TerminalReason}, map[string]string{"semantic_kind": string(epistemic.SemanticWorkIncomplete)})
				}
			}
		}
		m.syncWork()
		if !msg.plan.Initial {
			m.record("", event.Event{Source: "QAC", Kind: event.KindQAC, Message: fmt.Sprintf("%s %s → %s  %.2f / %.2f", strings.ToUpper(string(msg.plan.Action)), strings.ToUpper(msg.plan.Decision.From), strings.ToUpper(msg.plan.Decision.To), msg.plan.Decision.Score, msg.plan.Decision.Threshold)}, m.qacMeta(msg.plan))
		}
		return m, nil
	case agentdetail.SteeringSubmittedMsg:
		m.record(msg.AgentID, event.Event{Source: strings.ToUpper(msg.AgentID), Kind: event.KindSteering, Message: "steering updated: " + msg.Text}, nil)
		if s := m.sessions[msg.AgentID]; s != nil {
			input := runtime.Input{Text: msg.Text}
			var cmd tea.Cmd
			if s.State() == runtime.StateRunning {
				cmd = func() tea.Msg {
					return sessionErrorMsg{agentID: msg.AgentID, err: s.Steer(context.Background(), input)}
				}
			} else {
				cmd = func() tea.Msg { return sessionErrorMsg{agentID: msg.AgentID, err: s.Send(context.Background(), input)} }
			}
			return m, cmd
		}
		return m, nil
	case agentdetail.BackMsg:
		m.screen = DashboardScreen
		return m, nil
	case agentdetail.InterruptMsg:
		if s := m.sessions[msg.AgentID]; s != nil {
			return m, func() tea.Msg { return sessionErrorMsg{agentID: msg.AgentID, err: s.Interrupt(context.Background())} }
		}
	case sessionErrorMsg:
		if msg.err != nil {
			m.record(msg.agentID, event.Event{Source: strings.ToUpper(msg.agentID), Kind: event.KindError, Message: msg.err.Error()}, nil)
		}
		return m, nil
	}

	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		if key.Matches(keyMsg, m.keys.ForceQuit) && (!m.InputFocused() || !m.HasSteeringText()) {
			return m, m.quitCmd()
		}
		if key.Matches(keyMsg, m.keys.Quit) && !m.InputFocused() {
			return m, m.quitCmd()
		}
	}

	var cmd tea.Cmd
	if m.screen == AgentDetailScreen {
		m.detail, cmd = m.detail.Update(msg)
	} else if m.screen == EpistemicScreen {
		m.epistemic, cmd = m.epistemic.Update(msg)
	} else {
		m.dashboard, cmd = m.dashboard.Update(msg)
	}
	return m, cmd
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
	if m.cognition == nil {
		return
	}
	work := m.cognition.Work()
	if work == nil {
		m.dashboard = m.dashboard.SetWork("", "")
		m.detail = m.detail.SetWork("")
		return
	}
	state := string(work.State)
	if m.cesStore != nil {
		state = string(m.cesStore.State().Task.Status)
	}
	m.dashboard = m.dashboard.SetQAC(true, work.OwnerResource).SetWork(state, work.Goal)
	m.detail = m.detail.SetWork(state)
}

func (m Model) OperatorView() (epistemic.OperatorView, bool) {
	if m.cesStore == nil {
		return epistemic.OperatorView{}, false
	}
	return m.cesStore.OperatorView(), true
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

// qacMeta names the agents a cognition decision concerns. QAC plans address
// resources; history is keyed on agents, so the bindings are resolved here.
func (m *Model) qacMeta(plan cognition.Plan) map[string]string {
	meta := map[string]string{}
	if plan.WorkID != "" {
		meta[history.MetaWork] = plan.WorkID
	}
	if m.cognition == nil {
		return meta
	}
	if agent, ok := m.cognition.Agent(plan.From); ok {
		meta[history.MetaFrom] = agent
	}
	if agent, ok := m.cognition.Agent(plan.To); ok {
		meta[history.MetaTo] = agent
	}
	return meta
}

func (m Model) dispatchCognition(plan cognition.Plan) tea.Cmd {
	if plan.Action == "stop" {
		return func() tea.Msg { return cognitionResultMsg{plan: plan} }
	}
	agent, ok := m.cognition.Agent(plan.To)
	if !ok {
		return func() tea.Msg {
			return cognitionResultMsg{plan: plan, err: fmt.Errorf("unknown qac destination %q", plan.To)}
		}
	}
	s := m.sessions[agent]
	if s == nil {
		return func() tea.Msg {
			return cognitionResultMsg{plan: plan, err: fmt.Errorf("qac destination %q is unavailable", plan.To)}
		}
	}
	if err := m.cognition.BeginDispatch(plan); err != nil {
		return func() tea.Msg { return cognitionResultMsg{plan: plan, err: err} }
	}
	text := plan.Goal
	if plan.Action == "continue" {
		text = "[QAC DECISION]\n\nRemain at the current cognitive tier.\n\nContinue investigating."
	} else if plan.Action == "escalate" || plan.Action == "release" {
		work := m.cognition.Work()
		if work != nil {
			text = cognition.BuildHandoff(*work, plan.Request, plan.Decision)
		}
	}
	return func() tea.Msg {
		if s.State() != runtime.StateIdle {
			return cognitionResultMsg{plan: plan, err: fmt.Errorf("qac destination %q is unavailable", plan.To)}
		}
		return cognitionResultMsg{plan: plan, err: s.Send(context.Background(), runtime.Input{Text: text})}
	}
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
		if m.codex != nil {
			_ = m.codex.Close()
		}
		// The trace is an on-disk artefact of the run; close it so the file is
		// released rather than left to process exit.
		if m.trace != nil {
			_ = m.trace.Close()
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

// record is the single path by which anything reaches the operator. It writes
// the run trace, appends to canonical history, and only then updates the
// projections, so no view can hold something history does not.
func (m *Model) record(agentID string, item event.Event, meta map[string]string) {
	if item.Time.IsZero() {
		item.Time = timeNow()
	}
	entry := historyEntry(agentID, item, meta)
	if m.trace != nil {
		traced := item
		traced.Metadata = entry.Metadata
		if agentID != "" {
			traced.Metadata = mergeMeta(entry.Metadata, map[string]string{"agent_id": agentID})
		}
		_ = m.trace.Write(traced)
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

// mergeMeta returns a new map holding base overlaid with extra; neither input
// is modified.
func mergeMeta(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

var timeNow = func() time.Time { return time.Now() }
