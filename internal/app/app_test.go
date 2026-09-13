package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	"github.com/haha-systems/ghost/internal/ui/agentdetail"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
)

func press(text string, code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: text, Code: code})
}

func TestNavigationDashboardToDetailAndBack(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	cmd := mustCmd(t, m, press("enter", tea.KeyEnter))
	m, _ = runCmd(t, m, cmd)
	if m.Screen() != AgentDetailScreen {
		t.Fatalf("screen = %v, want detail", m.Screen())
	}
	cmd = mustCmd(t, m, press("esc", tea.KeyEscape))
	m, _ = runCmd(t, m, cmd)
	if m.Screen() != DashboardScreen {
		t.Fatalf("screen = %v, want dashboard", m.Screen())
	}
}

func TestConfiguredAgentsDoNotUsePhaseZeroActivity(t *testing.T) {
	agents := config.AgentSet{"backend": {Runtime: "codex", WorkingDir: "."}}
	cfg := config.Default()
	cfg.Agents = &agents
	m := New(cfg, nil)
	if m.EventCount() != 0 {
		t.Fatalf("configured event count=%d, want 0", m.EventCount())
	}
	m, _ = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Text: "enter"}))
	if strings.Contains(m.View().Content, "Searching references...") {
		t.Fatal("configured detail view still contains fake activity")
	}
}

func TestDemoEventsOnlyBuildForUnconfiguredRuns(t *testing.T) {
	if got := demoEvents(time.Now()); len(got) != 2 {
		t.Fatalf("demo events = %d, want 2", len(got))
	}
}

func TestUnsupportedRuntimeDoesNotStartCodex(t *testing.T) {
	agents := config.AgentSet{"review": {Runtime: "claude", WorkingDir: "."}}
	cfg := config.Default()
	cfg.Agents = &agents
	m := New(cfg, nil)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil command")
	}
	msg := cmd()
	started, ok := msg.(sessionsStartedMsg)
	if !ok {
		t.Fatalf("message = %T, want sessionsStartedMsg", msg)
	}
	if len(started.sessions) != 0 {
		t.Fatalf("sessions = %#v, want none", started.sessions)
	}
	if err := started.errors["review"]; err == nil || !strings.Contains(err.Error(), "unsupported runtime") {
		t.Fatalf("error = %v, want unsupported runtime", err)
	}
}

func TestAgentSelectionChanges(t *testing.T) {
	m := New(config.Default(), nil)
	if m.dashboard.SelectedIndex() != 0 {
		t.Fatalf("initial selection = %d, want 0", m.dashboard.SelectedIndex())
	}
	m, _ = updateModel(t, m, press("j", 'j'))
	if m.dashboard.SelectedIndex() != 1 {
		t.Fatalf("selection after j = %d, want 1", m.dashboard.SelectedIndex())
	}
	m, _ = updateModel(t, m, press("k", 'k'))
	if m.dashboard.SelectedIndex() != 0 {
		t.Fatalf("selection after k = %d, want 0", m.dashboard.SelectedIndex())
	}
}

func TestGlobalSteeringGeneratesEvent(t *testing.T) {
	m := New(config.Default(), nil)
	initial := m.EventCount()
	// Focus cycles roster -> event stream -> steering -> roster. The focus
	// command is intentionally not executed because cursor animation is not
	// part of this state assertion.
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	if m.dashboard.Focus() != dashboard.FocusEvents {
		t.Fatalf("focus after first tab = %d, want event stream", m.dashboard.Focus())
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	if !m.InputFocused() {
		t.Fatal("global input is not focused after two tabs")
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	if m.dashboard.Focus() != dashboard.FocusAgents {
		t.Fatalf("focus after third tab = %d, want agent list", m.dashboard.Focus())
	}
	// Two more tabs return to the steering editor.
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	for _, r := range "reproduce?" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	m, cmd := updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	if m.dashboard.InputValue() != "" {
		t.Fatalf("global input after submit = %q, want empty", m.dashboard.InputValue())
	}
	m, _ = runCmd(t, m, cmd)
	if m.EventCount() != initial+1 {
		t.Fatalf("event count = %d, want %d", m.EventCount(), initial+1)
	}
}

func TestQACGlobalSteeringStartsEntryAgent(t *testing.T) {
	agents := config.AgentSet{"wraith": {Runtime: "codex", WorkingDir: "."}, "shade": {Runtime: "codex", WorkingDir: "."}, "veil": {Runtime: "codex", WorkingDir: "."}}
	cfg := config.Default()
	cfg.Agents = &agents
	cfg.QAC = config.QACConfig{Enabled: true, EntryResource: "wraith", DefaultImportance: .5, Policy: config.QACPolicyConfig{Type: "threshold", Hierarchy: []string{"wraith", "shade", "veil"}}, Resources: map[string]config.QACResourceConfig{"wraith": {Agent: "wraith", Capability: .3, Cost: .1, Scarcity: .05}, "shade": {Agent: "shade", Capability: .65, Cost: .3, Scarcity: .3}, "veil": {Agent: "veil", Capability: .95, Cost: .8, Scarcity: .95}}}
	m := New(cfg, nil)
	w := fake.NewSession("wraith")
	m.sessions = map[string]runtime.Session{"wraith": w, "shade": fake.NewSession("shade"), "veil": fake.NewSession("veil")}
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "fix race"})
	m, _ = runCmd(t, m, cmd)
	if w.LastSend != "fix race" || m.cognition.Work() == nil || m.cognition.Work().OwnerResource != "wraith" {
		t.Fatalf("send=%q work=%#v", w.LastSend, m.cognition.Work())
	}
}

func TestAgentSteeringGeneratesEvent(t *testing.T) {
	m := New(config.Default(), nil)
	cmd := mustCmd(t, m, press("enter", tea.KeyEnter))
	m, _ = runCmd(t, m, cmd)
	for _, r := range "inspect" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	m, cmd = updateModel(t, m, press("ctrl+enter", tea.KeyEnter))
	if m.detail.InputValue() != "" {
		t.Fatalf("agent input after submit = %q, want empty", m.detail.InputValue())
	}
	m, _ = runCmd(t, m, cmd)
	if m.EventCount() != 3 {
		t.Fatalf("event count = %d, want 3", m.EventCount())
	}
}

func TestDashboardCoalescesAdjacentResponses(t *testing.T) {
	m := New(config.Default(), nil)
	before := m.EventCount()
	m.appendEvent(event.Event{Source: "BACKEND", Kind: event.Kind("response"), Message: "Hello"})
	m.appendEvent(event.Event{Source: "BACKEND", Kind: event.Kind("response"), Message: " world"})
	if m.EventCount() != before+1 {
		t.Fatalf("event count=%d, want %d", m.EventCount(), before+1)
	}
	m.dashboard = m.dashboard.SetSize(100, 30)
	if !strings.Contains(m.View().Content, "Hello world") {
		t.Fatal("response chunks were not combined")
	}
}

func TestSessionStartupPopulatesAgentMetadata(t *testing.T) {
	agents := config.AgentSet{"backend": {Runtime: "codex", WorkingDir: "."}}
	cfg := config.Default()
	cfg.Agents = &agents
	m := New(cfg, nil)
	m, _ = updateModel(t, m, sessionsStartedMsg{sessions: map[string]runtime.Session{"backend": fake.NewSession("backend")}, errors: map[string]error{}})
	agent, ok := m.dashboard.AgentAt(0)
	if !ok {
		t.Fatal("missing configured agent")
	}
	if agent.Model != "fake" || agent.SessionID != "backend" || agent.Runtime != "00m 00s" {
		t.Fatalf("metadata=%#v", agent)
	}
}

func TestResizeAndNarrowTerminal(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	width, height := m.Dimensions()
	if width != 120 || height != 40 {
		t.Fatalf("dimensions = %dx%d, want 120x40", width, height)
	}
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 20, Height: 10})
	if got := m.View().Content; got == "" {
		t.Fatal("narrow terminal rendered empty content")
	}
}

func TestViewsContainRequiredShellRegions(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !m.View().AltScreen {
		t.Fatal("dashboard view is not configured for the alternate screen")
	}
	content := m.View().Content
	for _, want := range []string{"GHOST", "VEIL", "EVENT STREAM", "STEER ALL"} {
		if !strings.Contains(content, want) {
			t.Fatalf("dashboard view does not contain %q", want)
		}
	}
	m, _ = runCmd(t, m, mustCmd(t, m, press("enter", tea.KeyEnter)))
	if !m.View().AltScreen {
		t.Fatal("detail view is not configured for the alternate screen")
	}
	content = m.View().Content
	// The detail log is split so agent decisions are not scrolled away by
	// high-volume tool and command activity.
	for _, want := range []string{"GHOST / VEIL", "DECISIONS", "ACTIVITY", "STEER", "VEIL"} {
		if !strings.Contains(content, want) {
			t.Fatalf("detail view does not contain %q", want)
		}
	}
}

func TestViewsFillTerminalAndUseThemeBackground(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	assertFullscreenView(t, m, 100, 30)

	m, _ = runCmd(t, m, mustCmd(t, m, press("enter", tea.KeyEnter)))
	assertFullscreenView(t, m, 100, 30)
}

func TestHelpBindingTogglesHelp(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(t, m, press("?", '?'))
	if !strings.Contains(m.View().Content, "previous agent") {
		t.Fatal("help view did not show key descriptions")
	}
}

func TestHelpFitsMinimumRecommendedTerminal(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, press("?", '?'))
	if height := lipgloss.Height(m.View().Content); height > 24 {
		t.Fatalf("help view height = %d, want at most 24", height)
	}
}

func TestViewsFitMinimumRecommendedWidth(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, line := range strings.Split(m.View().Content, "\n") {
		if width := lipgloss.Width(line); width > 80 {
			t.Fatalf("dashboard line width = %d, want at most 80: %q", width, line)
		}
	}
	m, _ = runCmd(t, m, mustCmd(t, m, press("enter", tea.KeyEnter)))
	for _, line := range strings.Split(m.View().Content, "\n") {
		if width := lipgloss.Width(line); width > 80 {
			t.Fatalf("detail line width = %d, want at most 80: %q", width, line)
		}
	}
}

func TestPopulatedInputsRemainWithinWidthAfterResize(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	for _, r := range strings.Repeat("x", 100) {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for index, line := range strings.Split(m.View().Content, "\n") {
		if width := lipgloss.Width(line); width > 80 {
			t.Fatalf("dashboard resized line %d width = %d, want at most 80: %q", index, width, line)
		}
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	m, _ = runCmd(t, m, mustCmd(t, m, press("enter", tea.KeyEnter)))
	for _, r := range strings.Repeat("y", 100) {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, line := range strings.Split(m.View().Content, "\n") {
		if width := lipgloss.Width(line); width > 80 {
			t.Fatalf("detail resized line width = %d, want at most 80", width)
		}
	}
}

func TestCustomMessagesAreHandled(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, dashboard.OpenAgentMsg{Index: 2})
	if m.Screen() != AgentDetailScreen || m.detail.Agent().Callsign != "SHADE" {
		t.Fatal("open agent message did not select SHADE")
	}
	m, _ = updateModel(t, m, agentdetail.BackMsg{})
	if m.Screen() != DashboardScreen {
		t.Fatal("back message did not return to dashboard")
	}
}

func updateModel(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, next := model.Update(msg)
	return updated.(Model), next
}

func runCmd(t *testing.T, model Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	updated, next := model.Update(cmd())
	return updated.(Model), next
}

func mustCmd(t *testing.T, model Model, msg tea.Msg) tea.Cmd {
	t.Helper()
	_, next := model.Update(msg)
	if next == nil {
		t.Fatal("expected a command")
	}
	return next
}

func assertFullscreenView(t *testing.T, m Model, width, height int) {
	t.Helper()
	view := m.View()
	if view.BackgroundColor == nil {
		t.Fatal("view background color is nil")
	}
	if got, want := view.BackgroundColor, lipgloss.Color(m.theme.Colors.Background); got != want {
		t.Fatalf("view background color = %v, want %v", got, want)
	}
	if got := lipgloss.Width(view.Content); got != width {
		t.Fatalf("view width = %d, want %d", got, width)
	}
	if got := lipgloss.Height(view.Content); got != height {
		t.Fatalf("view height = %d, want %d", got, height)
	}
}

func TestResponseCoalescingStaysWithinTurn(t *testing.T) {
	m := New(config.Default(), nil)
	m.appendEvent(event.Event{Source: "A", Kind: event.KindResponse, Message: "one", SessionID: "s", TurnID: "t1"})
	m.appendEvent(event.Event{Source: "A", Kind: event.KindResponse, Message: "two", SessionID: "s", TurnID: "t2"})
	if m.EventCount() != 4 {
		t.Fatalf("event count=%d, want 4", m.EventCount())
	}
}
