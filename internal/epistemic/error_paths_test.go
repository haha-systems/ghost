package epistemic

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMarshalJSONReturnsError(t *testing.T) {
	for _, value := range []any{make(chan int), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := marshalJSON(value); err == nil {
			t.Fatalf("marshalJSON(%T) accepted invalid value", value)
		}
	}
}

func TestPrepareStatusEventRejectsMalformedObject(t *testing.T) {
	for _, kind := range []ObjectKind{ObjectHypothesis, ObjectUnknown, ObjectFrame} {
		t.Run(string(kind), func(t *testing.T) {
			payload, err := marshalJSON(ObjectCreatedPayload{Kind: kind, Object: json.RawMessage(`{"Status":42}`)})
			if err != nil {
				t.Fatal(err)
			}
			events := []Event{{Kind: EventObjectCreated, ObjectID: "object", Payload: payload}}
			store := &Store{}
			err = store.prepareStatusEvent(&events, "revision", time.Now(), kind, "object", "active")
			if err == nil || !strings.Contains(err.Error(), "decode") {
				t.Fatalf("error = %v", err)
			}
			if len(events) != 1 {
				t.Fatal("malformed object appended a status event")
			}
		})
	}
}

func TestCloneStateOwnsNestedData(t *testing.T) {
	state := State{Task: Task{LastTransition: &TransitionSummary{Reason: "original"}}, Frames: []Frame{{CompletionConditions: []string{"complete"}, DisconfirmationConditions: []string{"reject"}}}}
	copy := cloneState(state)
	copy.Task.LastTransition.Reason = "changed"
	copy.Frames[0].CompletionConditions[0] = "changed"
	copy.Frames[0].DisconfirmationConditions[0] = "changed"
	if state.Task.LastTransition.Reason != "original" || state.Frames[0].CompletionConditions[0] != "complete" || state.Frames[0].DisconfirmationConditions[0] != "reject" {
		t.Fatal("clone changed original state")
	}
}

func TestFailRunOverridesCompletion(t *testing.T) {
	store, err := NewStore("failed final trace")
	if err != nil {
		t.Fatal(err)
	}
	// Model the status immediately after the orchestrator completes the work.
	store.state.Task.Status = WorkComplete
	if err := store.FailRun(); err != nil {
		t.Fatal(err)
	}
	if store.State().Task.Status != WorkIncomplete || store.State().Task.TerminalReason != "phase_execution_failed" {
		t.Fatalf("task = %#v", store.State().Task)
	}
}
