package app

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/event"
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
		return m, nil
	case agentdetail.SteeringSubmittedMsg:
		m.appendEvent(event.Event{Source: strings.ToUpper(msg.AgentID), Kind: event.KindSteering, Message: "steering updated: " + msg.Text})
		return m, nil
	case agentdetail.BackMsg:
		m.screen = DashboardScreen
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
	m.events = append(m.events, item)
	m.dashboard = m.dashboard.SetEvents(m.events)
}

var timeNow = func() time.Time { return time.Now() }
