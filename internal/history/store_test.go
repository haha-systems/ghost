package history

import (
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/event"
)

func entry(agent string, kind event.Kind, message string) Entry {
	return Entry{Time: time.Now(), AgentID: agent, Kind: kind, Message: message}
}

func messages(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Message)
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// An agent's work is recorded whether or not its screen was ever open, which is
// the whole point of moving history out of the detail model.
func TestAgentHistoryIsRetainedForLaterInspection(t *testing.T) {
	s := New(0)
	s.Append(entry("veil", event.KindCommand, "go test ./..."))
	s.Append(entry("wraith", event.KindCommand, "go vet ./..."))
	s.Append(entry("veil", event.KindFile, "internal/runtime/session.go"))

	equal(t, messages(s.Agent("veil")), []string{"go test ./...", "internal/runtime/session.go"})
}

func TestAgentHistoriesDoNotLeak(t *testing.T) {
	s := New(0)
	s.Append(entry("wraith", event.KindResponse, "wraith reasoning"))
	s.Append(entry("veil", event.KindResponse, "veil reasoning"))

	for _, e := range s.Agent("veil") {
		if e.AgentID != "veil" {
			t.Fatalf("veil history contains %q", e.AgentID)
		}
	}
	if got := len(s.Agent("shade")); got != 0 {
		t.Fatalf("unrelated agent has %d entries", got)
	}
}

// Projecting repeatedly must not duplicate anything: opening and closing a
// screen is a read, not a write.
func TestRepeatedProjectionIsStable(t *testing.T) {
	s := New(0)
	s.Append(entry("veil", event.KindCommand, "go build ./..."))
	first := len(s.Agent("veil"))
	if second := len(s.Agent("veil")); second != first {
		t.Fatalf("projection changed between reads: %d then %d", first, second)
	}
}

func TestStreamedResponseCoalescesIntoOneEntry(t *testing.T) {
	s := New(0)
	for _, fragment := range []string{"Invest", "igating", " cancellation"} {
		e := entry("veil", event.KindResponse, fragment)
		e.SessionID, e.TurnID = "session-1", "turn-1"
		s.Append(e)
	}
	got := s.Agent("veil")
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %v", len(got), messages(got))
	}
	if got[0].Message != "Investigating cancellation" {
		t.Fatalf("got %q", got[0].Message)
	}
}

func TestResponsesInDifferentTurnsStaySeparate(t *testing.T) {
	s := New(0)
	first := entry("veil", event.KindResponse, "first")
	first.SessionID, first.TurnID = "session-1", "turn-1"
	second := entry("veil", event.KindResponse, "second")
	second.SessionID, second.TurnID = "session-1", "turn-2"
	s.Append(first)
	s.Append(second)

	equal(t, messages(s.Agent("veil")), []string{"first", "second"})
}

// A backend that re-delivers an identical fragment must not double it.
func TestRepeatedFragmentIsNotDuplicated(t *testing.T) {
	s := New(0)
	e := entry("veil", event.KindResponse, "same")
	e.SessionID, e.TurnID = "session-1", "turn-1"
	s.Append(e)
	s.Append(e)

	equal(t, messages(s.Agent("veil")), []string{"same"})
}

func TestAppendReportsCoalescing(t *testing.T) {
	s := New(0)
	e := entry("veil", event.KindResponse, "part")
	e.SessionID, e.TurnID = "session-1", "turn-1"
	if _, merged := s.Append(e); merged {
		t.Fatal("first fragment reported as a merge")
	}
	if _, merged := s.Append(e); !merged {
		t.Fatal("continuation was not reported as a merge")
	}
}

func TestHistoryIsBounded(t *testing.T) {
	s := New(10)
	for i := 0; i < 500; i++ {
		s.Append(entry("veil", event.KindCommand, "command"))
	}
	if got := s.Len(); got != 10 {
		t.Fatalf("retained %d entries, want 10", got)
	}
	if got := len(s.All()); got != 10 {
		t.Fatalf("All returned %d entries, want 10", got)
	}
}

// Oldest entries are the ones discarded, so the visible tail is the recent run.
func TestBoundedHistoryDiscardsOldestFirst(t *testing.T) {
	s := New(3)
	for _, msg := range []string{"one", "two", "three", "four"} {
		s.Append(entry("veil", event.KindCommand, msg))
	}
	equal(t, messages(s.All()), []string{"two", "three", "four"})
}

func TestQACEntryAppearsInBothAgentHistories(t *testing.T) {
	s := New(0)
	e := Entry{
		Time: time.Now(), AgentID: "", Kind: event.KindQAC,
		Message:  "ESCALATE WRAITH → SHADE",
		Metadata: map[string]string{MetaFrom: "wraith", MetaTo: "shade", MetaWork: "work-001"},
	}
	s.Append(e)

	if got := len(s.Agent("wraith")); got != 1 {
		t.Fatalf("wraith has %d qac entries, want 1", got)
	}
	if got := len(s.Agent("shade")); got != 1 {
		t.Fatalf("shade has %d qac entries, want 1", got)
	}
	if got := len(s.Agent("veil")); got != 0 {
		t.Fatalf("unrelated veil received %d qac entries", got)
	}
}

func TestQACMetadataIsRetained(t *testing.T) {
	s := New(0)
	s.Append(Entry{
		Kind:     event.KindQAC,
		Message:  "ESCALATE WRAITH → SHADE",
		Metadata: map[string]string{MetaFrom: "wraith", MetaTo: "shade", MetaWork: "work-001"},
	})
	got := s.Agent("wraith")[0]
	for key, want := range map[string]string{MetaFrom: "wraith", MetaTo: "shade", MetaWork: "work-001"} {
		if got.Meta(key) != want {
			t.Fatalf("metadata %q = %q, want %q", key, got.Meta(key), want)
		}
	}
}

// Callers hold copies; mutating a projection must not corrupt the run record.
func TestProjectionsDoNotAliasStoredState(t *testing.T) {
	s := New(0)
	s.Append(Entry{
		AgentID: "veil", Kind: event.KindQAC, Message: "original",
		Metadata: map[string]string{MetaFrom: "veil"},
		Raw:      []byte(`{"a":1}`),
	})
	got := s.Agent("veil")
	got[0].Message = "mutated"
	got[0].Metadata[MetaFrom] = "mutated"
	got[0].Raw[0] = 'X'

	after := s.Agent("veil")[0]
	if after.Message != "original" {
		t.Fatalf("message mutated through projection: %q", after.Message)
	}
	if after.Meta(MetaFrom) != "veil" {
		t.Fatalf("metadata mutated through projection: %q", after.Meta(MetaFrom))
	}
	if string(after.Raw) != `{"a":1}` {
		t.Fatalf("raw payload mutated through projection: %q", after.Raw)
	}
}

func TestReplaceLatestRewritesMostRecentResponse(t *testing.T) {
	s := New(0)
	s.Append(entry("veil", event.KindResponse, "first"))
	s.Append(entry("veil", event.KindCommand, "go test"))
	s.Append(entry("veil", event.KindResponse, "raw text with a request block"))

	if _, ok := s.ReplaceLatest("veil", event.KindResponse, "cleaned"); !ok {
		t.Fatal("replacement reported no match")
	}
	equal(t, messages(s.Agent("veil")), []string{"first", "go test", "cleaned"})
}

func TestReplaceLatestReportsNoMatch(t *testing.T) {
	s := New(0)
	if _, ok := s.ReplaceLatest("veil", event.KindResponse, "cleaned"); ok {
		t.Fatal("replacement reported a match in an empty history")
	}
}
