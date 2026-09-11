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

func TestNormalizeNotificationUsesStructuredSemanticsAndKeepsRaw(t *testing.T) {
	raw := []byte(`{"item":{"type":"commandExecution","command":"go test ./...","status":"completed"},"turnId":"t1"}`)
	e := normalizeNotification(notification{Method: "item/completed", Params: raw}, "agent", "session", "t1")
	if e.Kind != runtime.KindCommand || e.Summary != "go test ./... (completed)" {
		t.Fatalf("event=%#v", e)
	}
	if string(e.Raw) != string(raw) || e.Metadata["backend_method"] != "item/completed" {
		t.Fatalf("raw/metadata not preserved: %#v", e)
	}
}

func TestNormalizeNotificationUnknownRemainsInspectable(t *testing.T) {
	raw := []byte(`{"mystery":"value"}`)
	e := normalizeNotification(notification{Method: "future/event", Params: raw}, "a", "s", "t")
	if e.Summary != "future/event" || string(e.Raw) != string(raw) || e.Metadata["backend_method"] != "future/event" {
		t.Fatalf("event=%#v", e)
	}
}

func TestPresentationDuplicateOnlyCoalescesWithinCausalScope(t *testing.T) {
	a := runtime.Event{AgentID: "a", SessionID: "s", TurnID: "t1", Kind: runtime.KindMessage, Summary: "hi"}
	if !samePresentation(a, a) {
		t.Fatal("identical presentation should coalesce")
	}
	b := a
	b.TurnID = "t2"
	if samePresentation(a, b) {
		t.Fatal("different turns must remain distinct")
	}
	c := a
	c.SessionID = "s2"
	if samePresentation(a, c) {
		t.Fatal("different sessions must remain distinct")
	}
}
