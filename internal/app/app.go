// Package app owns Ghost's top-level Bubble Tea model and screen lifecycle.
package app

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/history"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/run"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/ui/agentdetail"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
	uiEpistemic "github.com/haha-systems/ghost/internal/ui/epistemic"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

type Screen int

const (
	DashboardScreen Screen = iota
	AgentDetailScreen
	EpistemicScreen
	ConversationScreen
	LegacyDashboardScreen
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
	// history records events for the screens. Views project from it, so an
	// agent's log survives its detail screen being closed.
	history       *history.Store
	dashboard     dashboard.Model
	detail        agentdetail.Model
	epistemic     uiEpistemic.Model
	sessions      map[string]runtime.Session
	started       map[string]time.Time
	controller    *run.Controller
	runEvents     <-chan run.Event
	runStatus     run.Status
	projection    run.WorkspaceSnapshot
	conversation  conversationState
	colorProfile  colorprofile.Profile
	workspaceName string
	returnScreen  Screen
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
	return NewWithWorkDir(cfg, logger, "")
}

// NewWithWorkDir starts the app controller with its durable event log rooted
// in the selected workspace. New remains available to tests and embedded use.
func NewWithWorkDir(cfg config.Config, logger *slog.Logger, workdir string) Model {
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
		config: cfg, logger: logger, theme: th, keys: keys, screen: ConversationScreen,
		events:    events,
		dashboard: dashboard.New(th, keys, agents, events, cfg.UI.SteeringSubmit),
		detail:    detail,
		epistemic: uiEpistemic.New(th, keys),
		history:   history.New(history.DefaultLimit),
		sessions:  map[string]runtime.Session{},
		started:   map[string]time.Time{},
	}

	// The demo roster's events are evidence too; seeding them keeps the
	// dashboard and any opened detail screen showing the same run.
	for _, item := range events {
		m.history.Append(historyEntry(strings.ToLower(item.Source), item, nil))
	}

	m.controller = run.New(cfg, logger, run.Options{WorkDir: workdir})
	m.runEvents, _ = m.controller.Subscribe()
	m.runStatus = m.controller.Status()
	m.workspaceName = workspaceLabel(workdir)
	m.conversation = newConversationState()
	m.refreshProjection()
	if m.runStatus.Enabled {
		m.dashboard = m.dashboard.SetQAC(true, "")
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

	for _, id := range config.OrderedAgentNames(*cfg.Agents, cfg.AgentOrder) {
		a := (*cfg.Agents)[id]
		out = append(out, ghostmodel.Agent{ID: id, Callsign: strings.ToUpper(id), Client: strings.ToUpper(a.Runtime), Runtime: "—", Model: a.Model, State: ghostmodel.AgentIdle, Activity: "waiting for input"})
	}

	return out
}

type sessionsStartedMsg struct {
	sessions map[string]runtime.Session
	errors   map[string]error
}

type sessionEventMsg struct{ event runtime.Event }

type runEventMsg struct{ event run.Event }

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.waitRunEvent(), func() tea.Msg {
		sessions, failures := m.controller.StartSessions()
		return sessionsStartedMsg{sessions: sessions, errors: failures}
	})
}

// Keep session configuration available to existing callers.
var makeSessionConfig = run.MakeSessionConfig

func waitSessionEvent(s runtime.Session) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-s.Events()
		if !ok {
			return nil
		}
		return sessionEventMsg{event: e}
	}
}

// Screen returns the current model screen.
func (m Model) Screen() Screen { return m.screen }

// Dimensions returns the current model width and height.
func (m Model) Dimensions() (int, int) { return m.width, m.height }

// EventCount returns the number of model events.
func (m Model) EventCount() int { return len(m.events) }

// InputFocused returns whether the input field is focused.
func (m Model) InputFocused() bool {
	if m.screen == ConversationScreen {
		return m.conversation.focus == focusComposer
	}
	if m.screen == AgentDetailScreen {
		return m.detail.InputFocused()
	}
	return m.dashboard.InputFocused()
}

// HasSteeringText returns whether the input field has steering text.
func (m Model) HasSteeringText() bool {
	if m.screen == ConversationScreen {
		return m.conversation.composer != ""
	}
	if m.screen == AgentDetailScreen {
		return m.detail.InputValue() != ""
	}
	return m.dashboard.InputValue() != ""
}
