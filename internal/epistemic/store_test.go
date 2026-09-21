package epistemic

import (
	"reflect"
	"strings"
	"testing"
)

func TestCommitMintsIDsAndReplayRebuildsState(t *testing.T) {
	store, err := NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    Delta{Observations: []ObservationInput{{LocalRef: "raw", Content: "runtime received the instructions"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := result.IDs["raw"]
	if id == "" || id == ID("raw") {
		t.Fatalf("store returned non-canonical ID %q", id)
	}
	if got := store.State().Observations; len(got) != 1 || got[0].ID != id {
		t.Fatalf("observations = %#v", got)
	}

	events := store.Events()
	replayed, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.State(), replayed.State()) {
		t.Fatalf("replayed state differs\n got: %#v\nwant: %#v", replayed.State(), store.State())
	}
	if len(events) < 2 {
		t.Fatalf("event count = %d, want task creation and observation", len(events))
	}
}

func TestValidateCommitDoesNotMutateCanonicalState(t *testing.T) {
	store, err := NewStore("validate without mutation")
	if err != nil {
		t.Fatal(err)
	}
	before := len(store.Events())
	err = store.ValidateCommit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage_artifact"},
		Delta:    Delta{Observations: []ObservationInput{{LocalRef: "r4", Content: "evidence"}}, Claims: []ClaimInput{{LocalRef: "r4", Text: "collision"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate local ref") {
		t.Fatalf("validation error = %v, want duplicate local ref", err)
	}
	if got := len(store.Events()); got != before || len(store.State().Observations) != 0 || len(store.State().Claims) != 0 {
		t.Fatalf("validation mutated canonical state: events=%d observations=%d claims=%d", got, len(store.State().Observations), len(store.State().Claims))
	}
}

func TestCommitStoresTypedObjectsAndDerivedObservationProvenance(t *testing.T) {
	store, err := NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1", Deterministic: true, Transform: "extract-version", Version: "1"},
		Delta: Delta{
			Observations: []ObservationInput{
				{LocalRef: "raw", Content: "thread/start includes instructions"},
				{LocalRef: "derived", Content: "instructions present", DerivedFrom: "raw"},
			},
			Claims:    []ClaimInput{{LocalRef: "claim", Text: "the runtime received the prompt"}},
			Unknowns:  []UnknownInput{{LocalRef: "unknown", Question: "does the provider retain it?"}},
			Relations: []RelationInput{{LocalRef: "supports", Kind: RelationSupports, Source: "raw", Target: "claim"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := store.State()
	if len(state.Observations) != 2 || len(state.Claims) != 1 || len(state.Unknowns) != 1 {
		t.Fatalf("state object counts: observations=%d claims=%d unknowns=%d", len(state.Observations), len(state.Claims), len(state.Unknowns))
	}
	if len(state.Relations) != 2 || state.Relations[0].Kind != RelationDerivedFrom || state.Relations[0].SourceID != result.IDs["derived"] || state.Relations[0].TargetID != result.IDs["raw"] {
		t.Fatalf("derived provenance = %#v", state.Relations)
	}
	if state.Observations[0].ID == state.Observations[1].ID || state.Observations[0].RevisionID == "" {
		t.Fatal("objects do not have distinct canonical IDs and revisions")
	}
}

func TestCommitRejectsObservationMutation(t *testing.T) {
	store, err := NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    Delta{Observations: []ObservationInput{{LocalRef: "raw", Content: "original"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := "changed"
	_, err = store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-2"},
		Delta:    Delta{StatusChanges: []StatusChange{{Kind: ObjectObservation, Ref: string(first.IDs["raw"]), Status: status}}},
	})
	if err == nil {
		t.Fatal("observation mutation was accepted")
	}
}

func TestCommitRejectsHypothesisCreationDuringTriage(t *testing.T) {
	store, err := NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    Delta{Hypotheses: []HypothesisInput{{LocalRef: "h1", Mechanism: "the provider drops the prompt"}}},
	})
	if err == nil {
		t.Fatal("triage was allowed to create a causal hypothesis")
	}
}

func TestCommitRejectsExistingButUnprojectedReference(t *testing.T) {
	store, err := NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    Delta{Observations: []ObservationInput{{LocalRef: "raw", Content: "provider response"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-2", Deterministic: true, Transform: "extract", Version: "1"},
		Delta:    Delta{Observations: []ObservationInput{{LocalRef: "derived", Content: "derived result", DerivedFrom: string(first.IDs["raw"])}}},
	})
	if err == nil {
		t.Fatal("commit accepted a reference outside its projection")
	}
}
