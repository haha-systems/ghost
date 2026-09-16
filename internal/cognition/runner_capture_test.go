package cognition

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
)

const captureTriage = `{"classification":"defect","next_investigation":"capture the boundary"}`

// Real TRIAGE output: evidence gathered as observations, then cited by claims.
const observedTriage = `{
  "classification": "defect",
  "observations": [
    {"local_ref": "o1", "content": "the runner ignores item/completed messages"},
    {"local_ref": "o2", "content": "the codex normalizer classifies item/completed agentMessage as a message"}
  ],
  "claims": [{"local_ref": "c1", "text": "final messages are dropped at the runner", "evidence_ref": "o2"}],
  "unknowns": [{"local_ref": "u1", "question": "does codex always send deltas"}],
  "next_investigation": "inspect the runner's message filter",
  "qac_request": {"direction": "release", "uncertainty": 0.1, "expected_gain": 0.1, "reason": "cheaper model can abduce"}
}`

const markdownTriage = "## Triage\n\n**Classification:** defect\n\n- o1: the runner ignores item/completed messages\n"

func delta(text string) runtime.Event {
	return runtime.Event{TurnID: "turn-1", Kind: runtime.KindMessage, Summary: text, Metadata: map[string]string{"backend_method": "item/agentMessage/delta"}}
}

func completedMessage(text string) runtime.Event {
	return runtime.Event{TurnID: "turn-1", Kind: runtime.KindMessage, Summary: text, Metadata: map[string]string{"backend_method": "item/completed"}}
}

func startedMessage() runtime.Event {
	return runtime.Event{TurnID: "turn-1", Kind: runtime.KindMessage, Summary: "agentMessage", Metadata: map[string]string{"backend_method": "item/started"}}
}

func turnCompleted() runtime.Event {
	return runtime.Event{TurnID: "turn-1", Kind: runtime.KindStatus, Summary: "turn completed", Metadata: map[string]string{"backend_method": "turn/completed"}}
}

func runScripted(t *testing.T, events ...runtime.Event) (PhaseResult, map[string]map[string]string, error) {
	t.Helper()
	store, err := epistemic.NewStore("capture the artifact")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseTriage})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]map[string]string{}
	runner := NewPhaseRunner(&eventRuntime{events: events}, func(context.Context, string, epistemic.Phase) (runtime.SessionConfig, error) {
		return runtime.SessionConfig{AgentID: "wraith"}, nil
	}).WithObserver(func(e PhaseEvent) {
		mu.Lock()
		defer mu.Unlock()
		seen[e.Name] = e.Fields
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, PhaseRequest{WorkID: string(store.State().Task.ID), Goal: "capture", Phase: epistemic.PhaseTriage, ResourceID: "wraith", Projection: projection})
	return result, seen, err
}

func TestRunnerCapturesCompletedMessageWithoutDeltas(t *testing.T) {
	result, seen, err := runScripted(t, startedMessage(), completedMessage(captureTriage), turnCompleted())
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Raw) != captureTriage {
		t.Fatalf("raw = %q", result.Raw)
	}
	if got := seen[PhaseArtifactReceived]["source"]; got != ArtifactSourceFinalMessage {
		t.Fatalf("artifact source = %q", got)
	}
}

func TestRunnerStillAcceptsDeltaOnlyOutput(t *testing.T) {
	half := len(captureTriage) / 2
	result, seen, err := runScripted(t, delta(captureTriage[:half]), delta(captureTriage[half:]), turnCompleted())
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Raw) != captureTriage {
		t.Fatalf("raw = %q", result.Raw)
	}
	if got := seen[PhaseArtifactReceived]["source"]; got != ArtifactSourceStreamedDeltas {
		t.Fatalf("artifact source = %q", got)
	}
}

// The completed message is the whole answer the deltas streamed. Using both
// would duplicate the artifact and make it unparseable.
func TestRunnerPrefersCompletedMessageOverDeltasWithoutDuplicating(t *testing.T) {
	result, seen, err := runScripted(t, delta(captureTriage), startedMessage(), completedMessage(captureTriage), turnCompleted())
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Raw) != captureTriage || strings.Count(string(result.Raw), "classification") != 1 {
		t.Fatalf("raw = %q", result.Raw)
	}
	if got := seen[PhaseArtifactReceived]["source"]; got != ArtifactSourceFinalMessage {
		t.Fatalf("artifact source = %q", got)
	}
}

func TestRunnerFailsImmediatelyOnMarkdownTriage(t *testing.T) {
	_, seen, err := runScripted(t, completedMessage(markdownTriage), turnCompleted())
	if err == nil {
		t.Fatal("markdown triage was accepted")
	}
	invalid, ok := seen[PhaseArtifactInvalid]
	if !ok {
		t.Fatalf("no parse failure recorded: %v", seen)
	}
	if invalid["error"] == "" || invalid["digest"] == "" || invalid["preview"] == "" || invalid["source"] != ArtifactSourceFinalMessage {
		t.Fatalf("parse failure record = %v", invalid)
	}
}

func TestTriageObservationsSupportClaimsInTheSameCommit(t *testing.T) {
	artifact, err := ParsePhaseArtifact(epistemic.PhaseTriage, []byte(observedTriage))
	if err != nil {
		t.Fatal(err)
	}
	if got := artifact.QACRequest().Direction; got != "release" {
		t.Fatalf("qac direction = %q", got)
	}
	d, err := artifact.Delta()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Observations) != 2 || len(d.Claims) != 1 || len(d.Unknowns) != 1 {
		t.Fatalf("delta = %+v", d)
	}
	store, err := epistemic.NewStore("capture the artifact")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseTriage})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Commit(epistemic.CommitRequest{
		Phase:      epistemic.PhaseTriage,
		Producer:   epistemic.Producer{Phase: epistemic.PhaseTriage, Process: "wraith", Artifact: "triage_artifact", Runtime: "codex"},
		Delta:      d,
		Projection: projection,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := store.State()
	if len(state.Observations) != 2 || len(state.Claims) != 1 {
		t.Fatalf("state = %d observations, %d claims", len(state.Observations), len(state.Claims))
	}
	supported := false
	for _, relation := range state.Relations {
		if relation.Kind == epistemic.RelationSupports && relation.SourceID == result.IDs["o2"] && relation.TargetID == result.IDs["c1"] {
			supported = true
		}
	}
	if !supported {
		t.Fatalf("o2 does not support c1: %+v", state.Relations)
	}
	for _, observation := range state.Observations {
		if observation.ProducedBy.Process != "wraith" || observation.ProducedBy.Phase != epistemic.PhaseTriage {
			t.Fatalf("observation provenance = %+v", observation.ProducedBy)
		}
	}
}

func TestTriageContractCarriesTheExactSchema(t *testing.T) {
	instructions, err := phaseInstruction(epistemic.PhaseTriage)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Return ONLY one JSON object", "No\nMarkdown", `"observations"`, `"evidence_ref": "required: o1`, `"qac_request"`, "overrides the normal instruction"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("triage contract lacks %q:\n%s", want, instructions)
		}
	}
}

// eventRuntime starts sessions that answer a turn with a fixed event script.
type eventRuntime struct{ events []runtime.Event }

func (r *eventRuntime) Name() string                       { return "event-test" }
func (r *eventRuntime) Capabilities() runtime.Capabilities { return runtime.Capabilities{} }
func (r *eventRuntime) Close() error                       { return nil }
func (r *eventRuntime) Start(_ context.Context, config runtime.SessionConfig) (runtime.Session, error) {
	return &eventSession{agent: config.AgentID, script: r.events, events: make(chan runtime.Event, len(r.events))}, nil
}

type eventSession struct {
	agent  string
	script []runtime.Event
	events chan runtime.Event
}

func (s *eventSession) ID() string                  { return "event-session" }
func (s *eventSession) State() runtime.SessionState { return runtime.StateIdle }
func (s *eventSession) Send(context.Context, runtime.Input) error {
	for _, e := range s.script {
		e.AgentID, e.SessionID = s.agent, s.ID()
		s.events <- e
	}
	return nil
}
func (s *eventSession) Steer(context.Context, runtime.Input) error { return nil }
func (s *eventSession) Interrupt(context.Context) error            { return nil }
func (s *eventSession) Events() <-chan runtime.Event               { return s.events }
func (s *eventSession) Stats() runtime.SessionStats                { return runtime.SessionStats{} }
func (s *eventSession) Metadata() runtime.SessionMetadata {
	return runtime.SessionMetadata{ThreadID: s.ID(), Model: "test"}
}
func (s *eventSession) Close() error { return nil }
