package app

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
)

// rosterModel starts a run with three agents so history isolation between them
// can be exercised.
func rosterModel(t *testing.T) Model {
	t.Helper()
	agents := config.AgentSet{
		"veil":   {Runtime: "codex", WorkingDir: "."},
		"wraith": {Runtime: "codex", WorkingDir: "."},
		"shade":  {Runtime: "codex", WorkingDir: "."},
	}
	cfg := config.Default()
	cfg.Agents = &agents
	m := New(cfg, nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = updateModel(t, m, sessionsStartedMsg{
		sessions: map[string]runtime.Session{
			"veil":   fake.NewSession("veil"),
			"wraith": fake.NewSession("wraith"),
			"shade":  fake.NewSession("shade"),
		},
		errors: map[string]error{},
	})
	return m
}

func emit(t *testing.T, m Model, agentID string, kind runtime.EventKind, summary string) Model {
	t.Helper()
	m, _ = updateModel(t, m, sessionEventMsg{event: runtime.Event{
		AgentID: agentID, Kind: kind, Summary: summary,
		SessionID: "session-" + agentID, TurnID: "turn-1",
	}})
	return m
}

// openAgent opens the detail screen for one agent by its roster position.
func openAgent(t *testing.T, m Model, agentID string) Model {
	t.Helper()
	for i := 0; ; i++ {
		agent, ok := m.dashboard.AgentAt(i)
		if !ok {
			t.Fatalf("agent %q is not in the roster", agentID)
		}
		if agent.ID == agentID {
			m, _ = updateModel(t, m, dashboard.OpenAgentMsg{Index: i})
			return m
		}
	}
}

// The defect this phase exists to fix: work performed while the dashboard was
// showing used to be invisible when the agent was opened afterwards.
func TestHistorySurvivesAClosedDetailScreen(t *testing.T) {
	m := rosterModel(t)
	m = emit(t, m, "veil", runtime.KindCommand, "go test ./internal/runtime/...")
	m = emit(t, m, "veil", runtime.KindFile, "internal/runtime/session.go")

	m = openAgent(t, m, "veil")
	view := m.View().Content
	for _, want := range []string{"go test ./internal/runtime/...", "internal/runtime/session.go"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail view is missing retained history %q", want)
		}
	}
}

// Work done while a *different* agent's screen was open must still be recorded.
func TestHistoryRecordedWhileAnotherAgentIsOpen(t *testing.T) {
	m := rosterModel(t)
	m = openAgent(t, m, "wraith")
	m = emit(t, m, "veil", runtime.KindCommand, "veil ran the suite")

	m = openAgent(t, m, "veil")
	if !strings.Contains(m.View().Content, "veil ran the suite") {
		t.Fatal("veil history was lost while wraith was open")
	}
}

func TestSwitchingAgentsDoesNotLeakHistory(t *testing.T) {
	m := rosterModel(t)
	m = emit(t, m, "wraith", runtime.KindCommand, "wraith-only-command")
	m = emit(t, m, "veil", runtime.KindCommand, "veil-only-command")

	m = openAgent(t, m, "wraith")
	if !strings.Contains(m.View().Content, "wraith-only-command") {
		t.Fatal("wraith detail is missing its own history")
	}
	m = openAgent(t, m, "veil")
	view := m.View().Content
	if strings.Contains(view, "wraith-only-command") {
		t.Fatal("wraith history leaked into veil's detail screen")
	}
	if !strings.Contains(view, "veil-only-command") {
		t.Fatal("veil detail is missing its own history")
	}
}

// Opening a screen is a read. Doing it repeatedly must not duplicate rows.
func TestReopeningAnAgentDoesNotDuplicateHistory(t *testing.T) {
	m := rosterModel(t)
	m = emit(t, m, "veil", runtime.KindCommand, "unique-command")
	// A later event moves the sidebar's current-activity line off the string
	// being counted, so only the history pane can contribute to the count.
	m = emit(t, m, "veil", runtime.KindFile, "some/other/file.go")

	m = openAgent(t, m, "veil")
	m, _ = updateModel(t, m, press("esc", tea.KeyEscape))
	m = openAgent(t, m, "veil")

	if got := strings.Count(m.View().Content, "unique-command"); got != 1 {
		t.Fatalf("history row appears %d times in the view, want 1", got)
	}
	entries := 0
	for _, e := range m.history.Agent("veil") {
		if e.Message == "unique-command" {
			entries++
		}
	}
	if entries != 1 {
		t.Fatalf("canonical history holds %d copies, want 1", entries)
	}
}

func TestStreamedResponseStaysOneEntryInHistory(t *testing.T) {
	m := rosterModel(t)
	for _, fragment := range []string{"Invest", "igating", " cancellation"} {
		m = emit(t, m, "veil", runtime.KindMessage, fragment)
	}
	entries := m.history.Agent("veil")
	responses := 0
	for _, e := range entries {
		if e.Kind == event.KindResponse {
			responses++
			if e.Message != "Investigating cancellation" {
				t.Fatalf("response = %q", e.Message)
			}
		}
	}
	if responses != 1 {
		t.Fatalf("history holds %d responses, want 1", responses)
	}
}

// Canonical history and the dashboard projection must agree about how many rows
// a streamed response occupies.
func TestDashboardAndHistoryAgreeOnCoalescing(t *testing.T) {
	m := rosterModel(t)
	before := m.EventCount()
	for _, fragment := range []string{"one ", "two ", "three"} {
		m = emit(t, m, "veil", runtime.KindMessage, fragment)
	}
	if got := m.EventCount() - before; got != 1 {
		t.Fatalf("dashboard gained %d rows for one streamed response, want 1", got)
	}
}

func TestQACDecisionLandsInBothAgentHistories(t *testing.T) {
	m := rosterModel(t)
	m.record("", event.Event{Source: "QAC", Kind: event.KindQAC, Message: "ESCALATE WRAITH → SHADE"},
		map[string]string{"from": "wraith", "to": "shade", "work": "work-001"})

	for _, agentID := range []string{"wraith", "shade"} {
		if got := len(m.history.Agent(agentID)); got == 0 {
			t.Fatalf("%s did not receive the qac decision", agentID)
		}
	}
	for _, e := range m.history.Agent("veil") {
		if e.Kind == event.KindQAC {
			t.Fatal("unrelated agent received the qac decision")
		}
	}
}

func TestHistoryEntryPreservesSemanticEventMetadata(t *testing.T) {
	item := event.Event{Kind: event.KindReopen, Message: "work reopened", Metadata: map[string]string{"from": "execute", "to": "abduce", "reason": "new evidence"}}
	entry := historyEntry("", item, nil)
	if entry.Meta("from") != "execute" || entry.Meta("to") != "abduce" || entry.Meta("reason") != "new evidence" {
		t.Fatalf("semantic metadata = %#v", entry.Metadata)
	}
	item.Metadata["reason"] = "mutated after append"
	if entry.Meta("reason") != "new evidence" {
		t.Fatal("history entry shares the event metadata map")
	}
}

func TestQACDecisionIsVisibleOnBothDetailScreens(t *testing.T) {
	m := rosterModel(t)
	m.record("", event.Event{Source: "QAC", Kind: event.KindQAC, Message: "escalated WRAITH to SHADE"},
		map[string]string{"from": "wraith", "to": "shade"})

	for _, agentID := range []string{"wraith", "shade"} {
		opened := openAgent(t, m, agentID)
		if !strings.Contains(opened.View().Content, "escalated WRAITH to SHADE") {
			t.Fatalf("%s detail is missing the qac decision", agentID)
		}
	}
}

// The trace is the durable record and must keep every fragment, even where the
// interactive history coalesces them.
func TestTraceIsUnaffectedByCoalescing(t *testing.T) {
	dir := t.TempDir()
	agents := config.AgentSet{"veil": {Runtime: "codex", WorkingDir: "."}}
	cfg := config.Default()
	cfg.Agents = &agents
	cfg.Trace.Path = dir + "/trace.jsonl"
	m := New(cfg, nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = updateModel(t, m, sessionsStartedMsg{
		sessions: map[string]runtime.Session{"veil": fake.NewSession("veil")},
		errors:   map[string]error{},
	})
	for _, fragment := range []string{"alpha", "beta", "gamma"} {
		m = emit(t, m, "veil", runtime.KindMessage, fragment)
	}
	if m.trace != nil {
		_ = m.trace.Close()
	}

	raw, err := os.ReadFile(cfg.Trace.Path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	for _, fragment := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(string(raw), fragment) {
			t.Fatalf("trace lost fragment %q", fragment)
		}
	}
}
