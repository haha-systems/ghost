package codex

import (
	"strings"
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

func TestRateLimitNotificationSummaryShowsQuotaRemaining(t *testing.T) {
	p := map[string]any{"rateLimits": map[string]any{
		"primary":   map[string]any{"usedPercent": float64(45), "windowDurationMins": float64(300), "resetsAt": float64(1789074196)},
		"secondary": map[string]any{"usedPercent": float64(12), "windowDurationMins": float64(10080), "resetsAt": float64(1789497418)},
		"credits":   map[string]any{"hasCredits": false, "unlimited": false, "balance": "0"},
	}}
	got := notificationSummary("account/rateLimits/updated", p)
	for _, want := range []string{"primary: 55% remaining (45% used)", "secondary: 88% remaining (12% used)", "credits: balance 0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary=%q, missing %q", got, want)
		}
	}
}

func TestRateLimitNotificationKeepsQuotaMetadata(t *testing.T) {
	e := normalizeNotification(notification{Method: "account/rateLimits/updated", Params: []byte(`{
		"rateLimits":{"primary":{"usedPercent":45,"windowDurationMins":300,"resetsAt":1789074196}}
	}`)}, "agent", "session", "")
	if e.Metadata["rate_primary_remaining_percent"] != "55" || e.Metadata["rate_primary_window_minutes"] != "300" {
		t.Fatalf("metadata=%#v", e.Metadata)
	}
}

func TestSessionMergesSparseRateLimitUpdates(t *testing.T) {
	g := runtime.NewGuard()
	g.Ready()
	s := &Session{agentID: "agent", threadID: "session", guard: g, events: make(chan runtime.Event, 2)}
	s.handle(notification{Method: "account/rateLimits/updated", Params: []byte(`{
		"rateLimits":{"primary":{"usedPercent":45}}
	}`)})
	<-s.events
	s.handle(notification{Method: "account/rateLimits/updated", Params: []byte(`{
		"rateLimits":{"secondary":{"usedPercent":12}}
	}`)})
	e := <-s.events
	if !strings.Contains(e.Summary, "primary: 55% remaining") || !strings.Contains(e.Summary, "secondary: 88% remaining") {
		t.Fatalf("summary=%q", e.Summary)
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

func TestStartedNotificationReplacesLocalTurnPlaceholder(t *testing.T) {
	g := runtime.NewGuard()
	g.Ready()
	if err := g.StartTurn("turn-placeholder"); err != nil {
		t.Fatal(err)
	}
	s := &Session{agentID: "wraith", threadID: "thread", guard: g, events: make(chan runtime.Event, 1)}
	s.handle(notification{Method: "turn/started", Params: []byte(`{"turn":{"id":"backend-turn"}}`)})
	e := <-s.events
	if e.TurnID != "backend-turn" || g.ActiveTurn() != "backend-turn" {
		t.Fatalf("event turn=%q active turn=%q, want backend-turn", e.TurnID, g.ActiveTurn())
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

func TestSessionHandleLeavesUnscopedEventsWithoutTurnID(t *testing.T) {
	g := runtime.NewGuard()
	g.Ready()
	if err := g.StartTurn("turn-placeholder"); err != nil {
		t.Fatal(err)
	}
	s := &Session{agentID: "agent", threadID: "thread", guard: g, events: make(chan runtime.Event, 1)}
	s.handle(notification{
		Method: "mcpServer/startupStatus/updated",
		Params: []byte(`{"threadId":"thread","status":"ready"}`),
	})
	e := <-s.events
	if e.TurnID != "" {
		t.Fatalf("turn id=%q, want empty for an unscoped event", e.TurnID)
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
