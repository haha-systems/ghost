// Package app owns Ghost's top-level Bubble Tea model and screen lifecycle.
package app

import (
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
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
	return Model{
		config: cfg, logger: logger, theme: th, keys: keys, screen: DashboardScreen,
		events:    events,
		dashboard: dashboard.New(th, keys, agents, events),
		detail:    agentdetail.New(th, keys),
	}
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

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Screen() Screen { return m.screen }

func (m Model) Dimensions() (int, int) { return m.width, m.height }

func (m Model) EventCount() int { return len(m.events) }

func (m Model) InputFocused() bool {
	if m.screen == AgentDetailScreen {
		return m.detail.InputFocused()
	}
	return m.dashboard.InputFocused()
}
