package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/ui/agentdetail"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionsStartedMsg:
		m.sessions = msg.sessions
		for id, err := range msg.errors {
			m.appendEvent(event.Event{Source: strings.ToUpper(id), Kind: event.KindError, Message: err.Error()})
		}
		for id, s := range msg.sessions {
			meta := s.Metadata()
			m.dashboard = m.dashboard.UpdateAgentMetadata(id, meta.Model, meta.ThreadID)
		}
		cmds := make([]tea.Cmd, 0, len(msg.sessions))
		for _, s := range msg.sessions {
			cmds = append(cmds, waitSessionEvent(s))
		}
		return m, tea.Batch(cmds...)
	case sessionEventMsg:
		e := msg.event
		m.appendEvent(event.Event{Time: e.Time, Source: strings.ToUpper(e.AgentID), Kind: dashboardEventKind(e.Kind), Message: e.Summary})
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
			if m.detail.Agent().ID == e.AgentID {
				if e.Kind == runtime.KindMessage {
					m.detail = m.detail.AppendLog(e.Summary)
				} else {
					m.detail = m.detail.AddTypedLog(logType(e.Kind), e.Summary)
				}
			}
		}
		if m.cognition != nil {
			if plan, err := m.cognition.Observe(context.Background(), e, m.sessions, time.Now()); err != nil {
				m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
			} else if plan != nil {
				m.replaceLatestResponse(e.AgentID, plan.Visible)
				return m, tea.Batch(waitSessionEvent(m.sessions[e.AgentID]), m.dispatchCognition(*plan))
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
		return m, nil
	case dashboard.OpenAgentMsg:
		if agent, ok := m.dashboard.AgentAt(msg.Index); ok {
			m.detail = m.detail.SetAgent(agent).SetSize(m.width, m.height)
			m.screen = AgentDetailScreen
		}
		return m, nil
	case dashboard.SteeringSubmittedMsg:
		m.appendEvent(event.Event{Source: "SYSTEM", Kind: event.KindSteering, Message: "global steering updated: " + msg.Text})
		if m.cognition != nil && m.cognition.Work() == nil {
			plan, err := m.cognition.StartWork(msg.Text)
			if err != nil {
				return m, nil
			}
			return m, m.dispatchCognition(plan)
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
	case cognitionResultMsg:
		if msg.err != nil {
			m.cognition.Fail(msg.plan)
			m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: msg.err.Error()})
			return m, nil
		}
		if err := m.cognition.Commit(msg.plan, time.Now()); err != nil {
			m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
		}
		work := m.cognition.Work()
		if work != nil {
			m.dashboard = m.dashboard.SetQAC(true, work.OwnerResource)
		}
		if !msg.plan.Initial {
			m.appendEvent(event.Event{Source: "QAC", Kind: event.KindQAC, Message: fmt.Sprintf("%s %s → %s  %.2f / %.2f", strings.ToUpper(string(msg.plan.Action)), strings.ToUpper(msg.plan.Decision.From), strings.ToUpper(msg.plan.Decision.To), msg.plan.Decision.Score, msg.plan.Decision.Threshold)})
		}
		return m, nil
	case agentdetail.SteeringSubmittedMsg:
		m.appendEvent(event.Event{Source: strings.ToUpper(msg.AgentID), Kind: event.KindSteering, Message: "steering updated: " + msg.Text})
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
			m.appendEvent(event.Event{Source: strings.ToUpper(msg.agentID), Kind: event.KindError, Message: msg.err.Error()})
		}
		return m, nil
	}

	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		if key.Matches(keyMsg, m.keys.ForceQuit) {
			return m, m.quitCmd()
		}
		if key.Matches(keyMsg, m.keys.Quit) && !m.InputFocused() {
			return m, m.quitCmd()
		}
	}

	var cmd tea.Cmd
	if m.screen == AgentDetailScreen {
		m.detail, cmd = m.detail.Update(msg)
	} else {
		m.dashboard, cmd = m.dashboard.Update(msg)
	}
	return m, cmd
}

func (m *Model) replaceLatestResponse(agent, text string) {
	for i := len(m.events) - 1; i >= 0; i-- {
		if m.events[i].Source == strings.ToUpper(agent) && m.events[i].Kind == event.KindResponse {
			m.events[i].Message = text
			m.dashboard = m.dashboard.SetEvents(m.events)
			break
		}
	}
	if m.detail.Agent().ID == agent {
		m.detail = m.detail.ReplaceLatestResponse(text)
	}
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

func dashboardEventKind(kind runtime.EventKind) event.Kind {
	if kind == runtime.KindMessage {
		return event.KindResponse
	}
	if kind == runtime.KindError {
		return event.KindError
	}
	if kind == runtime.KindStatus || kind == runtime.KindSession {
		return event.KindStatus
	}
	return event.KindAgent
}

func logType(kind runtime.EventKind) string {
	switch kind {
	case runtime.KindThinking:
		return "thought"
	case runtime.KindCommand:
		return "command"
	case runtime.KindFile:
		return "file"
	case runtime.KindTool:
		return "tool"
	case runtime.KindUsage:
		return "usage"
	case runtime.KindError:
		return "error"
	default:
		return "event"
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
		return tea.QuitMsg{}
	}
}

func (m *Model) appendEvent(item event.Event) {
	if item.Time.IsZero() {
		item.Time = timeNow()
	}
	if item.Kind == event.KindResponse && len(m.events) > 0 {
		last := &m.events[len(m.events)-1]
		if last.Kind == event.KindResponse && last.Source == item.Source {
			last.Message += item.Message
			m.dashboard = m.dashboard.SetEvents(m.events)
			return
		}
	}
	m.events = append(m.events, item)
	if m.trace != nil {
		_ = m.trace.Write(item)
	}
	m.dashboard = m.dashboard.SetEvents(m.events)
}

var timeNow = func() time.Time { return time.Now() }
