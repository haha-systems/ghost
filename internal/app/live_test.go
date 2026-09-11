package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func configuredModel(t *testing.T) Model {
	t.Helper()
	agents := config.AgentSet{"backend": {Runtime: "codex", WorkingDir: "."}}
	cfg := config.Default()
	cfg.Agents = &agents
	m := New(cfg, nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = updateModel(t, m, sessionsStartedMsg{
		sessions: map[string]runtime.Session{"backend": fake.NewSession("backend")},
		errors:   map[string]error{},
	})
	return m
}

// The detail screen used to snapshot its agent when opened, leaving status,
// session, and activity frozen for as long as it stayed open.
func TestDetailMetadataStaysLiveWhileOpen(t *testing.T) {
	m := configuredModel(t)
	m, _ = runCmd(t, m, mustCmd(t, m, press("enter", tea.KeyEnter)))
	if m.Screen() != AgentDetailScreen {
		t.Fatal("did not open the detail screen")
	}

	m, _ = updateModel(t, m, sessionEventMsg{event: runtime.Event{
		AgentID: "backend", SessionID: "thread-7", Kind: runtime.KindCommand,
		Summary: "go test ./...",
	}})

	agent := m.detail.Agent()
	if agent.SessionID != "thread-7" {
		t.Fatalf("detail session = %q, want thread-7", agent.SessionID)
	}
	if agent.Activity != "go test ./..." {
		t.Fatalf("detail activity = %q", agent.Activity)
	}
	if !strings.Contains(m.View().Content, "thread-7") {
		t.Fatal("detail sidebar does not show the live session id")
	}
}

// The runtime column was written once and then left stale for the whole run.
func TestRuntimeColumnTicks(t *testing.T) {
	m := configuredModel(t)
	started := m.started["backend"]
	if started.IsZero() {
		t.Fatal("session start time was not recorded")
	}
	m, _ = updateModel(t, m, tickMsg(started.Add(75*time.Second)))
	agent, ok := m.dashboard.AgentAt(0)
	if !ok {
		t.Fatal("missing agent")
	}
	if agent.Runtime != "01m 15s" {
		t.Fatalf("runtime = %q, want 01m 15s", agent.Runtime)
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "00m 00s"},
		{9 * time.Second, "00m 09s"},
		{75 * time.Second, "01m 15s"},
		{time.Hour + 3*time.Minute, "1h 03m"},
		{-time.Second, "00m 00s"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.d); got != c.want {
			t.Fatalf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// Backend kinds must survive to the stream instead of collapsing to "agent".
func TestEventKindsSurviveToTheStream(t *testing.T) {
	cases := map[runtime.EventKind]string{
		runtime.KindCommand:  "command",
		runtime.KindFile:     "file",
		runtime.KindTool:     "tool",
		runtime.KindThinking: "thinking",
		runtime.KindUsage:    "usage",
		runtime.KindMessage:  "response",
		runtime.KindError:    "error",
	}
	for kind, want := range cases {
		if got := string(dashboardEventKind(kind)); got != want {
			t.Fatalf("dashboardEventKind(%q) = %q, want %q", kind, got, want)
		}
	}
}
