package cognition

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
)

// scriptSession replays a fixed event script after Send and then stays open,
// so each test shapes exactly how far a phase run gets.
type scriptSession struct {
	id     string
	script []runtime.Event
	events chan runtime.Event
}

func (s *scriptSession) ID() string                  { return s.id }
func (s *scriptSession) State() runtime.SessionState { return runtime.StateRunning }
func (s *scriptSession) Send(context.Context, runtime.Input) error {
	for _, e := range s.script {
		s.events <- e
	}
	return nil
}
func (s *scriptSession) Steer(context.Context, runtime.Input) error { return nil }
func (s *scriptSession) Interrupt(context.Context) error            { return nil }
func (s *scriptSession) Events() <-chan runtime.Event               { return s.events }
func (s *scriptSession) Stats() runtime.SessionStats                { return runtime.SessionStats{} }
func (s *scriptSession) Metadata() runtime.SessionMetadata {
	return runtime.SessionMetadata{ThreadID: s.id, Model: "script"}
}
func (s *scriptSession) Close() error { return nil }

type scriptRuntime struct {
	script []runtime.Event
	block  bool
}

func (r *scriptRuntime) Name() string                       { return "script" }
func (r *scriptRuntime) Capabilities() runtime.Capabilities { return runtime.Capabilities{} }
func (r *scriptRuntime) Close() error                       { return nil }
func (r *scriptRuntime) Start(ctx context.Context, _ runtime.SessionConfig) (runtime.Session, error) {
	if r.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &scriptSession{id: "thread-1", script: r.script, events: make(chan runtime.Event, len(r.script)+1)}, nil
}

type recorder struct {
	mu     sync.Mutex
	events []PhaseEvent
}

func (r *recorder) observe(e PhaseEvent) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) find(name string) (PhaseEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Name == name {
			return e, true
		}
	}
	return PhaseEvent{}, false
}

func observedRun(t *testing.T, rt runtime.Runtime, timeout, stall time.Duration) (*recorder, error) {
	t.Helper()
	store, err := epistemic.NewStore("make VisibleThroughSeq visible")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseTriage})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	runner := NewPhaseRunner(rt, func(context.Context, string, epistemic.Phase) (runtime.SessionConfig, error) {
		return runtime.SessionConfig{AgentID: "wraith"}, nil
	}).WithObserver(rec.observe).WithStallTimeout(stall)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err = runner.Run(ctx, PhaseRequest{WorkID: "work-1", Phase: epistemic.PhaseTriage, ResourceID: "wraith", Projection: projection})
	return rec, err
}

func ev(kind runtime.EventKind, turn, method, summary string, raw string) runtime.Event {
	e := runtime.Event{SessionID: "thread-1", TurnID: turn, Kind: kind, Summary: summary, Metadata: map[string]string{"backend_method": method}}
	if raw != "" {
		e.Raw = []byte(raw)
	}
	return e
}

func TestTimeoutDiagnosticsNameHowFarThePhaseGot(t *testing.T) {
	cases := []struct {
		name  string
		rt    *scriptRuntime
		check func(t *testing.T, f map[string]string)
	}{
		{
			name: "session never started",
			rt:   &scriptRuntime{block: true},
			check: func(t *testing.T, f map[string]string) {
				if f["stage"] != StageSessionStarting {
					t.Fatalf("stage = %q", f["stage"])
				}
			},
		},
		{
			name: "turn never started",
			rt:   &scriptRuntime{},
			check: func(t *testing.T, f map[string]string) {
				if f["stage"] != StageTurnRequested || f["events"] != "0" {
					t.Fatalf("fields = %v", f)
				}
			},
		},
		{
			name: "tool hung",
			rt: &scriptRuntime{script: []runtime.Event{
				ev(runtime.KindStatus, "turn-a", "turn/started", "turn started", ""),
				ev(runtime.KindCommand, "turn-a", "item/started", "rg VisibleThroughSeq .", `{"item":{"id":"item-1","type":"commandExecution"}}`),
			}},
			check: func(t *testing.T, f map[string]string) {
				if f["stage"] != StageActive || f["open_items"] != "1" || f["open_item_summaries"] != "command: rg VisibleThroughSeq ." {
					t.Fatalf("fields = %v", f)
				}
			},
		},
		{
			name: "output without completion",
			rt: &scriptRuntime{script: []runtime.Event{
				ev(runtime.KindMessage, "turn-a", "item/agentMessage/delta", `{"classification":`, ""),
				ev(runtime.KindMessage, "turn-a", "item/agentMessage/delta", `"defect"}`, ""),
			}},
			check: func(t *testing.T, f map[string]string) {
				if f["stage"] != StageActive || f["message_deltas"] != "2" || f["open_items"] != "0" {
					t.Fatalf("fields = %v", f)
				}
			},
		},
		{
			name: "completion carried a different turn id",
			rt: &scriptRuntime{script: []runtime.Event{
				ev(runtime.KindStatus, "turn-placeholder", "thread/status/changed", "active", ""),
				ev(runtime.KindMessage, "turn-real", "item/agentMessage/delta", `{}`, ""),
				ev(runtime.KindStatus, "turn-real", "turn/completed", "turn completed", ""),
			}},
			check: func(t *testing.T, f map[string]string) {
				if f["ignored_events"] != "2" {
					t.Fatalf("fields = %v", f)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := observedRun(t, tc.rt, 50*time.Millisecond, 0)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v", err)
			}
			timeout, ok := rec.find(PhaseTimeout)
			if !ok {
				t.Fatal("no phase_timeout event")
			}
			if timeout.RunID == "" || timeout.Phase != "triage" || timeout.ResourceID != "wraith" || timeout.Fields["time_since_last_activity_ms"] == "" {
				t.Fatalf("timeout event = %+v", timeout)
			}
			if _, stalled := rec.find(PhaseStalled); stalled {
				t.Fatal("a timeout was reported as a stall")
			}
			finished, ok := rec.find(PhaseFinished)
			if !ok || finished.Fields["outcome"] != "error" {
				t.Fatalf("finish = %+v", finished)
			}
			tc.check(t, timeout.Fields)
		})
	}
	// The mismatched completion is named, not silently dropped.
	rec, _ := observedRun(t, cases[4].rt, 50*time.Millisecond, 0)
	ignored, ok := rec.find(PhaseEventIgnored)
	if !ok || ignored.Fields["reason"] != "turn_mismatch" || ignored.Fields["expected"] != "turn-placeholder" {
		t.Fatalf("ignored = %+v", ignored)
	}
}

func TestStallIsDistinctFromTimeout(t *testing.T) {
	rt := &scriptRuntime{script: []runtime.Event{
		ev(runtime.KindThinking, "turn-a", "item/reasoning/delta", "thinking", ""),
	}}
	rec, err := observedRun(t, rt, 5*time.Second, 40*time.Millisecond)
	if !errors.Is(err, ErrPhaseStalled) {
		t.Fatalf("err = %v", err)
	}
	stalled, ok := rec.find(PhaseStalled)
	if !ok || stalled.Fields["last_event_kind"] != string(runtime.KindThinking) || stalled.Fields["last_event_summary"] != "thinking" || stalled.TurnID != "turn-a" || stalled.SessionID != "thread-1" {
		t.Fatalf("stall = %+v", stalled)
	}
	if _, timedOut := rec.find(PhaseTimeout); timedOut {
		t.Fatal("a stall was reported as a timeout")
	}
}

func TestOpenCommandWaitsForLifecycleCompletionInsteadOfStalling(t *testing.T) {
	rt := &scriptRuntime{script: []runtime.Event{
		ev(runtime.KindStatus, "turn-a", "turn/started", "turn started", ""),
		ev(runtime.KindCommand, "turn-a", "item/started", "go test ./internal/store/postgres", `{"item":{"id":"item-1","type":"commandExecution"}}`),
	}}
	rec, err := observedRun(t, rt, 100*time.Millisecond, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want enclosing phase timeout", err)
	}
	if _, stalled := rec.find(PhaseStalled); stalled {
		t.Fatal("an open command was reported as stalled")
	}
	timeout, ok := rec.find(PhaseTimeout)
	if !ok {
		t.Fatal("no phase_timeout event")
	}
	if timeout.Fields["open_items"] != "1" || timeout.Fields["open_item_summaries"] != "command: go test ./internal/store/postgres" {
		t.Fatalf("timeout = %+v", timeout)
	}
}

func TestEveryEventOfOneRunSharesItsRunID(t *testing.T) {
	rt := &scriptRuntime{script: []runtime.Event{
		ev(runtime.KindCommand, "turn-a", "item/started", "rg x", `{"item":{"id":"i1"}}`),
		ev(runtime.KindCommand, "turn-a", "item/completed", "rg x (completed)", `{"item":{"id":"i1"}}`),
		ev(runtime.KindMessage, "turn-a", "item/agentMessage/delta", `{"classification":"defect","next_investigation":"look"}`, ""),
		ev(runtime.KindStatus, "turn-a", "turn/completed", "turn completed", ""),
	}}
	rec, err := observedRun(t, rt, 5*time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	runID := rec.events[0].RunID
	for _, e := range rec.events {
		if e.RunID != runID || e.WorkID != "work-1" || e.AgentID == "" && e.Name != PhaseSessionStarting {
			t.Fatalf("event %s correlation = %+v", e.Name, e)
		}
	}
	parsed, ok := rec.find(PhaseArtifactParsed)
	if !ok || parsed.SessionID != "thread-1" || parsed.TurnID != "turn-a" {
		t.Fatalf("parsed = %+v", parsed)
	}
	if again, _ := observedRun(t, rt, 5*time.Second, 0); again.events[0].RunID == runID {
		t.Fatal("a second run reused the run id")
	}
}
