package app

import (
	"testing"

	"github.com/haha-systems/ghost/internal/run"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

// A session that CES restarted replaces the old handle, and the old session's
// last event does not start a second reader on the new one.
func TestRestartedSessionReplacesTheOldReader(t *testing.T) {
	m := configuredModel(t)
	old := m.sessions["backend"]
	restarted := fake.NewSession("backend")
	m, cmd := updateModel(t, m, runEventMsg{event: run.Event{AgentID: "backend", RestartedSession: &run.SessionRestart{AgentID: "backend", Session: restarted}}})
	if m.sessions["backend"] != restarted || cmd == nil {
		t.Fatalf("restarted session was not installed (cmd %v)", cmd != nil)
	}
	if _, next := updateModel(t, m, sessionEventMsg{event: runtime.Event{AgentID: "backend", Kind: runtime.KindStatus}, session: old}); next != nil {
		t.Fatal("the replaced session's event started another reader")
	}
	if _, next := updateModel(t, m, sessionEventMsg{event: runtime.Event{AgentID: "backend", Kind: runtime.KindStatus}, session: restarted}); next == nil {
		t.Fatal("the restarted session's reader stopped")
	}
}
