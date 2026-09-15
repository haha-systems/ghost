package cognition

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
)

func TestPhaseRunnerStartsFreshSessionAndSendsProjectionAndInstructions(t *testing.T) {
	store, err := epistemic.NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(epistemic.CommitRequest{
		Phase:    epistemic.PhaseTriage,
		Producer: epistemic.Producer{Phase: epistemic.PhaseTriage, Process: "triage", Artifact: "seed"},
		Delta:    epistemic.Delta{Observations: []epistemic.ObservationInput{{LocalRef: "o1", Content: "prompt was sent"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseTriage})
	if err != nil {
		t.Fatal(err)
	}

	rt := &phaseTestRuntime{outputs: []string{
		`{"classification":"prompt delivery","next_investigation":"capture the provider request"}`,
		`{"hypotheses":[{"local_ref":"h1","mechanism":"the runtime drops instructions","falsifier":"provider capture lacks instructions"}],"leading_hypothesis_ref":"h1"}`,
	}}
	runner := NewPhaseRunner(rt, func(_ context.Context, resource string, _ epistemic.Phase) (runtime.SessionConfig, error) {
		return runtime.SessionConfig{AgentID: resource, Instructions: "global prompt\n\nagent soul"}, nil
	})
	request := PhaseRequest{WorkID: string(store.State().Task.ID), Goal: "restore prompt delivery", Phase: epistemic.PhaseTriage, ResourceID: "wraith", Projection: projection}
	firstResult, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.SessionID == "" || firstResult.TurnID == "" || firstResult.Artifact.Phase != epistemic.PhaseTriage {
		t.Fatalf("result = %#v", firstResult)
	}
	if _, err := store.TransitionPhase(epistemic.TransitionRequest{To: epistemic.PhaseAbduce, Reason: "triage artifact committed"}); err != nil {
		t.Fatal(err)
	}
	request.Phase = epistemic.PhaseAbduce
	request.Projection, err = store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseAbduce})
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.SessionID == "" || secondResult.TurnID == "" || secondResult.Artifact.Phase != epistemic.PhaseAbduce {
		t.Fatalf("result = %#v", secondResult)
	}
	if len(rt.sessions) != 2 || rt.sessions[0].ID() == rt.sessions[1].ID() {
		t.Fatalf("sessions were reused: %#v", rt.sessions)
	}
	first, second := rt.sessions[0], rt.sessions[1]
	if first.config.Instructions == second.config.Instructions || !strings.Contains(first.config.Instructions, "global prompt") || !strings.Contains(first.config.Instructions, "agent soul") || !strings.Contains(first.config.Instructions, "TRIAGE") || !strings.Contains(second.config.Instructions, "ABDUCE") {
		t.Fatalf("phase instructions = %q / %q", first.config.Instructions, second.config.Instructions)
	}
	if !strings.Contains(first.input, "restore prompt delivery") || !strings.Contains(first.input, "prompt was sent") || strings.Contains(second.input, "old transcript") {
		t.Fatalf("session input did not contain only task projection: %q / %q", first.input, second.input)
	}
	if !first.closed || !second.closed {
		t.Fatal("phase session was not closed")
	}
}

type phaseTestRuntime struct {
	mu       sync.Mutex
	outputs  []string
	sessions []*phaseTestSession
}

func (r *phaseTestRuntime) Name() string                       { return "phase-test" }
func (r *phaseTestRuntime) Capabilities() runtime.Capabilities { return runtime.Capabilities{} }
func (r *phaseTestRuntime) Start(_ context.Context, config runtime.SessionConfig) (runtime.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := len(r.sessions)
	if index >= len(r.outputs) {
		return nil, fmt.Errorf("no test output %d", index)
	}
	session := newPhaseTestSession(index, config, r.outputs[index])
	r.sessions = append(r.sessions, session)
	return session, nil
}
func (r *phaseTestRuntime) Close() error { return nil }

type phaseTestSession struct {
	config runtime.SessionConfig
	id     string
	output string
	events chan runtime.Event
	input  string
	closed bool
	mu     sync.Mutex
}

func newPhaseTestSession(index int, config runtime.SessionConfig, output string) *phaseTestSession {
	return &phaseTestSession{config: config, id: fmt.Sprintf("session-%d", index+1), output: output, events: make(chan runtime.Event, 2)}
}
func (s *phaseTestSession) ID() string                  { return s.id }
func (s *phaseTestSession) State() runtime.SessionState { return runtime.StateIdle }
func (s *phaseTestSession) Send(_ context.Context, input runtime.Input) error {
	s.mu.Lock()
	s.input = input.Text
	s.mu.Unlock()
	s.events <- runtime.Event{AgentID: s.config.AgentID, SessionID: s.id, TurnID: "turn-1", Kind: runtime.KindMessage, Summary: s.output, Metadata: map[string]string{"backend_method": "item/agentMessage/delta"}}
	s.events <- runtime.Event{AgentID: s.config.AgentID, SessionID: s.id, TurnID: "turn-1", Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}}
	return nil
}
func (s *phaseTestSession) Steer(context.Context, runtime.Input) error {
	return fmt.Errorf("unexpected steer")
}
func (s *phaseTestSession) Interrupt(context.Context) error {
	return fmt.Errorf("unexpected interrupt")
}
func (s *phaseTestSession) Events() <-chan runtime.Event { return s.events }
func (s *phaseTestSession) Stats() runtime.SessionStats  { return runtime.SessionStats{} }
func (s *phaseTestSession) Metadata() runtime.SessionMetadata {
	return runtime.SessionMetadata{ThreadID: s.id, Model: "test"}
}
func (s *phaseTestSession) Close() error { s.mu.Lock(); s.closed = true; s.mu.Unlock(); return nil }
