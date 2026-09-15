package epistemic

import "testing"

func TestCompleteRequiresConfirmedLeadingHypothesis(t *testing.T) {
	store, err := NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	triage, err := store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    Delta{Observations: []ObservationInput{{LocalRef: "o1", Content: "prompt was delivered"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []Phase{PhaseAbduce, PhaseFrame, PhaseExecute, PhaseClose} {
		if _, err := store.TransitionPhase(TransitionRequest{To: next, Reason: "phase complete"}); err != nil {
			t.Fatal(err)
		}
		if next == PhaseAbduce {
			projection, err := store.Project(ProjectionRequest{Phase: PhaseAbduce})
			if err != nil {
				t.Fatal(err)
			}
			result, err := store.Commit(CommitRequest{
				Phase:      PhaseAbduce,
				Producer:   Producer{Phase: PhaseAbduce, Process: "abducer", Artifact: "abduce-1"},
				Projection: projection,
				Delta:      Delta{Hypotheses: []HypothesisInput{{LocalRef: "h1", Mechanism: "runtime preserves instructions"}}, Relations: []RelationInput{{LocalRef: "supports", Kind: RelationSupports, Source: string(triage.IDs["o1"]), Target: "h1"}}, LeadingHypothesis: "h1"},
			})
			if err != nil {
				t.Fatal(err)
			}
			_ = result
		}
	}
	if _, err := store.SetTerminal(WorkComplete, ""); err == nil {
		t.Fatal("unconfirmed hypothesis was allowed to complete work")
	}
	state := store.State()
	for _, hypothesis := range state.Hypotheses {
		if hypothesis.ID == state.Task.LeadingHypothesis {
			projection, err := store.Project(ProjectionRequest{Phase: PhaseClose})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Commit(CommitRequest{Phase: PhaseClose, Producer: Producer{Phase: PhaseClose, Process: "closer", Artifact: "close-1"}, Projection: projection, Delta: Delta{StatusChanges: []StatusChange{{Kind: ObjectHypothesis, Ref: string(hypothesis.ID), Status: string(HypothesisConfirmed)}}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := store.SetTerminal(WorkComplete, ""); err != nil {
		t.Fatal(err)
	}
	if got := store.State().Task.Status; got != WorkComplete {
		t.Fatalf("status = %s", got)
	}
	if len(store.SemanticEvents()) == 0 {
		t.Fatal("terminal semantic event stream is empty")
	}
}
