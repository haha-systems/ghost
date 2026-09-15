package cognition

import (
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/epistemic"
)

func TestOrchestratorFollowsForwardFlowAndStopsUnderdeterminedWork(t *testing.T) {
	store, err := epistemic.NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := NewOrchestrator(store)
	for _, phase := range []epistemic.Phase{epistemic.PhaseAbduce, epistemic.PhaseFrame, epistemic.PhaseExecute, epistemic.PhaseClose} {
		got, err := orchestrator.Advance()
		if err != nil {
			t.Fatal(err)
		}
		if got != phase {
			t.Fatalf("phase = %s, want %s", got, phase)
		}
	}
	if _, err := orchestrator.Advance(); err != nil {
		t.Fatal(err)
	}
	state := store.State()
	if state.Task.Status != epistemic.WorkIncomplete || state.Task.TerminalReason != "underdetermined" {
		t.Fatalf("work terminal state = %s (%s)", state.Task.Status, state.Task.TerminalReason)
	}
}

func TestOrchestratorRoutesContradictionByTargetAndCountsReopens(t *testing.T) {
	store, hypothesisID := executableTask(t)
	projection, err := store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseExecute})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(epistemic.CommitRequest{
		Phase:      epistemic.PhaseExecute,
		Producer:   epistemic.Producer{Phase: epistemic.PhaseExecute, Process: "executor", Artifact: "execute-1"},
		Projection: projection,
		Delta: epistemic.Delta{
			Observations: []epistemic.ObservationInput{{LocalRef: "capture", Content: "provider capture omitted the instructions"}},
			Relations:    []epistemic.RelationInput{{LocalRef: "contradiction", Kind: epistemic.RelationContradicts, Source: "capture", Target: string(hypothesisID)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := NewOrchestrator(store)
	phase, err := orchestrator.Advance()
	if err != nil {
		t.Fatal(err)
	}
	state := store.State()
	if phase != epistemic.PhaseAbduce || state.Task.ReopenCount != 1 || state.Task.LastTransition.TargetID != hypothesisID {
		t.Fatalf("transition phase=%s count=%d last=%#v", phase, state.Task.ReopenCount, state.Task.LastTransition)
	}
	if !strings.Contains(state.Task.LastTransition.Reason, "provider capture omitted the instructions") {
		t.Fatalf("transition reason = %q", state.Task.LastTransition.Reason)
	}
	seenReopen := false
	for _, event := range store.SemanticEvents() {
		if event.Kind == epistemic.SemanticWorkReopened {
			seenReopen = true
		}
	}
	if !seenReopen {
		t.Fatal("semantic WorkReopened event is missing")
	}

	for i := 0; i < 3; i++ {
		for _, next := range []epistemic.Phase{epistemic.PhaseFrame, epistemic.PhaseExecute} {
			if _, err := store.TransitionPhase(epistemic.TransitionRequest{To: next, Reason: "phase work completed"}); err != nil {
				t.Fatal(err)
			}
		}
		phase, err = orchestrator.Advance()
		if err != nil {
			t.Fatal(err)
		}
	}
	state = store.State()
	if phase != epistemic.PhaseExecute || state.Task.Status != epistemic.WorkIncomplete || state.Task.TerminalReason != "reopen_budget_exhausted" || state.Task.ReopenCount != epistemic.MaxReopens {
		t.Fatalf("budget state phase=%s task=%#v", phase, state.Task)
	}
}

func TestQACStopLeavesWorkIncomplete(t *testing.T) {
	store, err := epistemic.NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := NewOrchestrator(store)
	if err := orchestrator.ResourceUnavailable(); err != nil {
		t.Fatal(err)
	}
	if got := store.State().Task.Status; got != epistemic.WorkIncomplete {
		t.Fatalf("work status = %s, want incomplete", got)
	}
}

func executableTask(t *testing.T) (*epistemic.Store, epistemic.ID) {
	t.Helper()
	store, err := epistemic.NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	triage, err := store.Commit(epistemic.CommitRequest{
		Phase:    epistemic.PhaseTriage,
		Producer: epistemic.Producer{Phase: epistemic.PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    epistemic.Delta{Observations: []epistemic.ObservationInput{{LocalRef: "o1", Content: "runtime received the instructions"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(epistemic.TransitionRequest{To: epistemic.PhaseAbduce, Reason: "triage complete"}); err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseAbduce})
	if err != nil {
		t.Fatal(err)
	}
	abduction, err := store.Commit(epistemic.CommitRequest{
		Phase:      epistemic.PhaseAbduce,
		Producer:   epistemic.Producer{Phase: epistemic.PhaseAbduce, Process: "abducer", Artifact: "abduce-1"},
		Projection: projection,
		Delta: epistemic.Delta{
			Hypotheses:        []epistemic.HypothesisInput{{LocalRef: "h1", Mechanism: "the runtime loses the prompt"}},
			Relations:         []epistemic.RelationInput{{LocalRef: "supports", Kind: epistemic.RelationSupports, Source: string(triage.IDs["o1"]), Target: "h1"}},
			LeadingHypothesis: "h1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(epistemic.TransitionRequest{To: epistemic.PhaseFrame, Reason: "leading hypothesis selected"}); err != nil {
		t.Fatal(err)
	}
	projection, err = store.Project(epistemic.ProjectionRequest{Phase: epistemic.PhaseFrame})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(epistemic.CommitRequest{
		Phase:      epistemic.PhaseFrame,
		Producer:   epistemic.Producer{Phase: epistemic.PhaseFrame, Process: "framer", Artifact: "frame-1"},
		Projection: projection,
		Delta:      epistemic.Delta{Frames: []epistemic.FrameInput{{LocalRef: "f1", Name: "prompt path", Summary: "verify prompt delivery"}}, Relations: []epistemic.RelationInput{{LocalRef: "depends", Kind: epistemic.RelationDependsOn, Source: "f1", Target: string(abduction.IDs["h1"])}}, ActiveFrame: "f1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(epistemic.TransitionRequest{To: epistemic.PhaseExecute, Reason: "frame approved"}); err != nil {
		t.Fatal(err)
	}
	return store, abduction.IDs["h1"]
}
