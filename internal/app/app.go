// Package app owns Ghost's top-level Bubble Tea model and screen lifecycle.
package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/codex"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/ui/agentdetail"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

type Screen int

const (
	DashboardScreen Screen = iota
	AgentDetailScreen
)

type Model struct {
	config config.Config
	logger *slog.Logger
	theme  theme.Theme
	keys   keymap.KeyMap

	screen    Screen
	width     int
	height    int
	events    []event.Event
	dashboard dashboard.Model
	detail    agentdetail.Model
	codex     *codex.Runtime
	sessions  map[string]runtime.Session
}

func New(cfg config.Config, logger *slog.Logger) Model {
	th := theme.Bloodwire()
	if configured := strings.TrimSpace(strings.ToLower(cfg.UI.Theme)); configured != "" && configured != "bloodwire" && logger != nil {
		logger.Warn("unknown theme; using Bloodwire", "theme", cfg.UI.Theme)
	}
	keys := keymap.Default()
	now := time.Now()
	events := []event.Event{
		{Time: now.Add(-5 * time.Second), Source: "VEIL", Kind: event.KindAgent, Message: "read internal/auth/session.go"},
		{Time: now.Add(-2 * time.Second), Source: "WRAITH", Kind: event.KindAgent, Message: "exec go test ./..."},
	}
	agents := configuredAgents(cfg)
	m := Model{
		config: cfg, logger: logger, theme: th, keys: keys, screen: DashboardScreen,
		events:    events,
		dashboard: dashboard.New(th, keys, agents, events),
		detail:    agentdetail.New(th, keys), sessions: map[string]runtime.Session{},
	}
	if cfg.Agents != nil {
		m.codex = codex.NewRuntime()
	}
	return m
}

func configuredAgents(cfg config.Config) []ghostmodel.Agent {
	if cfg.Agents == nil {
		return ghostmodel.MockAgents()
	}
	out := make([]ghostmodel.Agent, 0, len(*cfg.Agents))
	for id, a := range *cfg.Agents {
		out = append(out, ghostmodel.Agent{ID: id, Callsign: strings.ToUpper(id), Client: strings.ToUpper(a.Runtime), Runtime: "—", Model: a.Model, State: ghostmodel.AgentIdle, Activity: "waiting for input"})
	}
	return out
}

type sessionsStartedMsg struct {
	sessions map[string]runtime.Session
	errors   map[string]error
}

func (m Model) Init() tea.Cmd {
	if m.codex == nil || m.config.Agents == nil {
		return nil
	}
	return func() tea.Msg {
		out := sessionsStartedMsg{sessions: map[string]runtime.Session{}, errors: map[string]error{}}
		for id, c := range *m.config.Agents {
			s, e := m.codex.Start(context.Background(), runtime.SessionConfig{AgentID: id, WorkingDir: c.WorkingDir, Model: c.Model})
			if e != nil {
				out.errors[id] = e
			} else {
				out.sessions[id] = s
			}
		}
		return out
	}
}

func (m Model) Screen() Screen { return m.screen }

func (m Model) Dimensions() (int, int) { return m.width, m.height }

func (m Model) EventCount() int { return len(m.events) }

func (m Model) InputFocused() bool {
	if m.screen == AgentDetailScreen {
		return m.detail.InputFocused()
	}
	return m.dashboard.InputFocused()
}
