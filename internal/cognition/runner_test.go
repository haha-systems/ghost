package cognition

import (
	"context"
	"errors"
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

func TestPhaseRunnerRejectsArtifactWithInvalidProjectedEndpoint(t *testing.T) {
	projection := epistemic.Projection{Phase: epistemic.PhaseTriage, State: epistemic.State{
		Unknowns: []epistemic.Unknown{{ID: "ces_unknown", Question: "what happened?"}},
	}}
	rt := &phaseTestRuntime{outputs: []string{`{"classification":"bug","claims":[{"local_ref":"c1","text":"claim","evidence_ref":"ces_unknown"}],"next_investigation":"inspect"}`}}
	runner := NewPhaseRunner(rt, func(_ context.Context, resource string, _ epistemic.Phase) (runtime.SessionConfig, error) {
		return runtime.SessionConfig{AgentID: resource}, nil
	})
	_, err := runner.Run(context.Background(), PhaseRequest{WorkID: "work", Goal: "diagnose", Phase: epistemic.PhaseTriage, ResourceID: "wraith", Projection: projection})
	if err == nil || !strings.Contains(err.Error(), "cannot connect") {
		t.Fatalf("runner error = %v, want invalid relation endpoints", err)
	}
}

func TestPhaseRunnerRepairsSemanticallyInvalidArtifact(t *testing.T) {
	projection := epistemic.Projection{Phase: epistemic.PhaseAbduce, State: epistemic.State{
		Observations: []epistemic.Observation{{ID: "ces_observation", Content: "evidence"}},
		Unknowns:     []epistemic.Unknown{{ID: "ces_unknown", Question: "what remains?"}},
	}}
	rt := &phaseTestRuntime{
		outputs: []string{`{"hypotheses":[{"local_ref":"h1","mechanism":"cause","falsifier":"counterexample"}],"relations":[{"local_ref":"r1","kind":"supports","source":"ces_unknown","target":"h1"}],"leading_hypothesis_ref":"h1"}`},
		repairs: []string{`{"hypotheses":[{"local_ref":"h1","mechanism":"cause","falsifier":"counterexample"}],"relations":[{"local_ref":"r1","kind":"supports","source":"ces_observation","target":"h1"}],"leading_hypothesis_ref":"h1"}`},
	}
	runner := NewPhaseRunner(rt, func(_ context.Context, resource string, _ epistemic.Phase) (runtime.SessionConfig, error) {
		return runtime.SessionConfig{AgentID: resource}, nil
	})
	result, err := runner.Run(context.Background(), PhaseRequest{WorkID: "work", Goal: "diagnose", Phase: epistemic.PhaseAbduce, ResourceID: "wraith", Projection: projection})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.Phase != epistemic.PhaseAbduce {
		t.Fatalf("result = %#v", result)
	}
	session := rt.sessions[0]
	if len(session.inputs) != 2 || !strings.Contains(session.inputs[1], `cannot connect unknown to hypothesis`) {
		t.Fatalf("repair inputs = %#v", session.inputs)
	}
	source := session.schemas[0]["properties"].(map[string]any)["relations"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["source"].(map[string]any)
	if fmt.Sprint(source["enum"]) != "[ces_observation]" {
		t.Fatalf("structured source enum = %v", source["enum"])
	}
}

func TestPhaseRunnerRepairsCommitContractErrorInCurrentSession(t *testing.T) {
	store, err := epistemic.NewStore("repair commit contract")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(epistemic.CommitRequest{
		Phase:    epistemic.PhaseTriage,
		Producer: epistemic.Producer{Phase: epistemic.PhaseTriage, Process: "seed", Artifact: "seed"},
		Delta:    epistemic.Delta{Observations: []epistemic.ObservationInput{{LocalRef: "o1", Content: "evidence"}}},
	}); err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseTriage})
	if err != nil {
		t.Fatal(err)
	}
	evidenceRef := string(projection.State.Observations[0].ID)
	rt := &phaseTestRuntime{outputs: []string{
		`{"classification":"bug","claims":[{"local_ref":"c1","text":"claim"}],"next_investigation":"inspect"}`,
	}, repairs: []string{
		fmt.Sprintf(`{"classification":"bug","claims":[{"local_ref":"c1","text":"claim","evidence_ref":%q}],"next_investigation":"inspect"}`, evidenceRef),
	}}
	runner := NewPhaseRunner(rt, func(_ context.Context, resource string, _ epistemic.Phase) (runtime.SessionConfig, error) {
		return runtime.SessionConfig{AgentID: resource}, nil
	})
	result, err := runner.Run(context.Background(), PhaseRequest{
		WorkID: string(store.State().Task.ID), Goal: "repair", Phase: epistemic.PhaseTriage,
		ResourceID: "wraith", Projection: projection,
		ValidateArtifact: func(artifact PhaseArtifact) error {
			delta, err := artifact.Delta()
			if err != nil {
				return RepairableArtifactError(err)
			}
			err = store.ValidateCommit(epistemic.CommitRequest{
				Phase:    epistemic.PhaseTriage,
				Producer: epistemic.Producer{Phase: epistemic.PhaseTriage, Process: "wraith", Artifact: "triage_artifact"},
				Delta:    delta, Projection: projection,
			})
			if err != nil {
				return RepairableArtifactError(err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.Phase != epistemic.PhaseTriage || len(rt.sessions[0].inputs) != 2 {
		t.Fatalf("result/session = %#v, inputs=%#v", result, rt.sessions[0].inputs)
	}
	if !strings.Contains(rt.sessions[0].inputs[1], "has no supporting source") {
		t.Fatalf("repair prompt = %q", rt.sessions[0].inputs[1])
	}
}

func TestPhaseRunnerExhaustedRepairIsExplicit(t *testing.T) {
	projection := epistemic.Projection{Phase: epistemic.PhaseAbduce, State: epistemic.State{
		Observations: []epistemic.Observation{{ID: "ces_observation", Content: "evidence"}},
	}}
	invalid := `{"hypotheses":[{"local_ref":"h1","mechanism":"cause","falsifier":"counterexample"}],"relations":[{"local_ref":"r1","kind":"supports","source":"ces_observation","target":"ces_observation"}],"leading_hypothesis_ref":"h1"}`
	rt := &phaseTestRuntime{outputs: []string{invalid}, repairs: []string{invalid, invalid}}
	runner := NewPhaseRunner(rt, func(_ context.Context, resource string, _ epistemic.Phase) (runtime.SessionConfig, error) {
		return runtime.SessionConfig{AgentID: resource}, nil
	})
	_, err := runner.Run(context.Background(), PhaseRequest{WorkID: "work", Goal: "exhaust", Phase: epistemic.PhaseAbduce, ResourceID: "wraith", Projection: projection})
	var exhausted *ArtifactRepairExhaustedError
	if !errors.As(err, &exhausted) || !strings.Contains(err.Error(), "after 2 attempts") {
		t.Fatalf("error = %v, want explicit repair exhaustion", err)
	}
}

func TestPhaseRunnerPromptIncludesTypedProjectionIndex(t *testing.T) {
	projection := epistemic.Projection{Phase: epistemic.PhaseFrame, State: epistemic.State{
		Observations: []epistemic.Observation{{ID: "ces_observation", Content: "evidence"}},
		Hypotheses:   []epistemic.Hypothesis{{ID: "ces_hypothesis", Mechanism: "cause"}},
		Constraints:  []epistemic.Constraint{{ID: "ces_constraint", Text: "constraint"}},
		Frames:       []epistemic.Frame{{ID: "ces_frame", Name: "old", Summary: "old frame"}},
	}}
	rt := &phaseTestRuntime{outputs: []string{`{"frame":{"local_ref":"f1","name":"new","summary":"new frame"}}`}}
	runner := NewPhaseRunner(rt, func(_ context.Context, resource string, _ epistemic.Phase) (runtime.SessionConfig, error) {
		return runtime.SessionConfig{AgentID: resource}, nil
	})
	_, err := runner.Run(context.Background(), PhaseRequest{WorkID: "work", Goal: "frame", Phase: epistemic.PhaseFrame, ResourceID: "wraith", Projection: projection})
	if err != nil {
		t.Fatal(err)
	}
	input := rt.sessions[0].input
	for _, want := range []string{"\"projection_index\"", "ces_hypothesis", "ces_constraint", "ces_frame", "hypothesis"} {
		if !strings.Contains(input, want) {
			t.Fatalf("prompt lacks %q: %s", want, input)
		}
	}
}

type phaseTestRuntime struct {
	mu       sync.Mutex
	outputs  []string
	repairs  []string
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
	outputs := []string{r.outputs[index]}
	if index == 0 {
		outputs = append(outputs, r.repairs...)
	}
	session := newPhaseTestSession(index, config, outputs)
	r.sessions = append(r.sessions, session)
	return session, nil
}
func (r *phaseTestRuntime) Close() error { return nil }

type phaseTestSession struct {
	config  runtime.SessionConfig
	id      string
	outputs []string
	events  chan runtime.Event
	input   string
	inputs  []string
	schemas []map[string]any
	sends   int
	closed  bool
	mu      sync.Mutex
}

func newPhaseTestSession(index int, config runtime.SessionConfig, outputs []string) *phaseTestSession {
	return &phaseTestSession{config: config, id: fmt.Sprintf("session-%d", index+1), outputs: outputs, events: make(chan runtime.Event, 2)}
}
func (s *phaseTestSession) ID() string                  { return s.id }
func (s *phaseTestSession) State() runtime.SessionState { return runtime.StateIdle }
func (s *phaseTestSession) Send(_ context.Context, input runtime.Input) error {
	s.mu.Lock()
	s.input = input.Text
	s.inputs = append(s.inputs, input.Text)
	s.schemas = append(s.schemas, input.OutputSchema)
	index := s.sends
	s.sends++
	s.mu.Unlock()
	if index >= len(s.outputs) {
		index = len(s.outputs) - 1
	}
	turnID := fmt.Sprintf("turn-%d", index+1)
	s.events <- runtime.Event{AgentID: s.config.AgentID, SessionID: s.id, TurnID: turnID, Kind: runtime.KindMessage, Summary: s.outputs[index], Metadata: map[string]string{"backend_method": "item/agentMessage/delta"}}
	s.events <- runtime.Event{AgentID: s.config.AgentID, SessionID: s.id, TurnID: turnID, Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}}
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
