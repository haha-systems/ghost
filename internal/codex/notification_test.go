package codex

import (
	"testing"

	"github.com/haha-systems/ghost/internal/runtime"
)

func TestNotificationSummaryUsesStructuredItemContent(t *testing.T) {
	got := notificationSummary("item/started", map[string]any{"item": map[string]any{"type": "commandExecution", "command": "go test ./..."}})
	if got != "go test ./..." {
		t.Fatalf("summary=%q", got)
	}
	got = notificationSummary("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "content": []any{map[string]any{"type": "text", "text": "hello"}}}})
	if got != "hello" {
		t.Fatalf("message=%q", got)
	}
}

func TestTurnIDReadsCompletedTurnShape(t *testing.T) {
	if got := turnID(map[string]any{"turn": map[string]any{"id": "t1"}}); got != "t1" {
		t.Fatalf("turn id=%q", got)
	}
}

func TestCompletedEventKeepsTurnIDAndBackendMethod(t *testing.T) {
	g := runtime.NewGuard()
	g.Ready()
	if err := g.StartTurn("t1"); err != nil {
		t.Fatal(err)
	}
	s := &Session{agentID: "wraith", threadID: "thread", guard: g, events: make(chan runtime.Event, 1)}
	s.handle(notification{Method: "turn/completed", Params: []byte(`{"turn":{"id":"t1"}}`)})
	e := <-s.events
	if e.TurnID != "t1" || e.Metadata["backend_method"] != "turn/completed" || s.State() != runtime.StateIdle {
		t.Fatalf("event=%#v state=%s", e, s.State())
	}
}
