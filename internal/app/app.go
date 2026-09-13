// Package app owns Ghost's top-level Bubble Tea model and screen lifecycle.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/codex"
	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/history"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/runtime"
	ghosttrace "github.com/haha-systems/ghost/internal/trace"
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

	screen Screen
	width  int
	height int
	events []event.Event
	// history is the canonical record of the run. Views project from it, so an
	// agent's log survives its detail screen being closed.
	history   *history.Store
	dashboard dashboard.Model
	detail    agentdetail.Model
	codex     *codex.Runtime
	sessions  map[string]runtime.Session
	started   map[string]time.Time
	trace     *ghosttrace.Writer
	cognition *cognition.Coordinator
	// workComplete records that QAC stopped escalating the current work item.
	workComplete bool
	// ticking guards against starting more than one elapsed-time ticker.
	ticking bool
}

// tickMsg drives the elapsed-time column, which was previously written once and
// then left stale for the rest of the run.
type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// formatElapsed renders a session age as the dashboard's runtime column.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	if h := total / 3600; h > 0 {
		return fmt.Sprintf("%dh %02dm", h, total%3600/60)
	}
	return fmt.Sprintf("%02dm %02ds", total/60, total%60)
}

func New(cfg config.Config, logger *slog.Logger) Model {
	th := theme.Bloodwire()
	if configured := strings.TrimSpace(strings.ToLower(cfg.UI.Theme)); configured != "" && configured != "bloodwire" && logger != nil {
		logger.Warn("unknown theme; using Bloodwire", "theme", cfg.UI.Theme)
	}
	keys := keymap.Default()
	now := time.Now()
	var events []event.Event
	if cfg.Agents == nil {
		events = demoEvents(now)
	}
	agents := configuredAgents(cfg)
	detail := agentdetail.New(th, keys, cfg.UI.SteeringSubmit)
	if cfg.Agents != nil {
		detail = detail.ClearLogs()
	}
	m := Model{
		config: cfg, logger: logger, theme: th, keys: keys, screen: DashboardScreen,
		events:    events,
		dashboard: dashboard.New(th, keys, agents, events, cfg.UI.SteeringSubmit),
		detail:    detail,
		history:   history.New(history.DefaultLimit),
		sessions:  map[string]runtime.Session{},
		started:   map[string]time.Time{},
	}
	// The demo roster's events are evidence too; seeding them keeps the
	// dashboard and any opened detail screen showing the same run.
	for _, item := range events {
		m.history.Append(historyEntry(strings.ToLower(item.Source), item, nil))
	}
	if writer, err := ghosttrace.Open(cfg.Trace.Path); err == nil {
		m.trace = writer
	} else if logger != nil {
		logger.Error("open trace", "error", err)
	}
	if cfg.QAC.Enabled {
		if c, err := cognition.New(cfg.QAC); err == nil {
			m.cognition = c
			m.dashboard = m.dashboard.SetQAC(true, "")
		} else if logger != nil {
			logger.Error("disable invalid qac", "error", err)
		}
	}
	if cfg.Agents != nil {
		m.codex = codex.NewRuntime()
	}
	return m
}

func demoEvents(now time.Time) []event.Event {
	return []event.Event{
		{Time: now.Add(-5 * time.Second), Source: "VEIL", Kind: event.KindAgent, Message: "read internal/auth/session.go"},
		{Time: now.Add(-2 * time.Second), Source: "WRAITH", Kind: event.KindAgent, Message: "exec go test ./..."},
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

type sessionsStartedMsg struct {
	sessions map[string]runtime.Session
	errors   map[string]error
}
type sessionEventMsg struct{ event runtime.Event }
type cognitionResultMsg struct {
	plan cognition.Plan
	err  error
}

func (m Model) Init() tea.Cmd {
	if m.codex == nil || m.config.Agents == nil {
		return nil
	}
	return (func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out := sessionsStartedMsg{sessions: map[string]runtime.Session{}, errors: map[string]error{}}
		for id, c := range *m.config.Agents {
			if c.Runtime != "codex" {
				out.errors[id] = fmt.Errorf("unsupported runtime %q", c.Runtime)
				continue
			}
			s, e := m.codex.Start(ctx, runtime.SessionConfig{AgentID: id, WorkingDir: c.WorkingDir, Model: c.Model})
			if e != nil {
				out.errors[id] = e
			} else {
				out.sessions[id] = s
			}
		}
		return out
	})
}

func waitSessionEvent(s runtime.Session) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-s.Events()
		if !ok {
			return nil
		}
		return sessionEventMsg{event: e}
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
func (m Model) HasSteeringText() bool {
	if m.screen == AgentDetailScreen {
		return m.detail.InputValue() != ""
	}
	return m.dashboard.InputValue() != ""
}
