package epistemic

import (
	"reflect"
	"testing"
)

func TestUnknownCanResolveReopenResolveAndRelationRetractionIsAppendOnly(t *testing.T) {
	store, err := NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    Delta{Unknowns: []UnknownInput{{LocalRef: "u1", Question: "does the provider retain the instructions?"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	unknownID := created.IDs["u1"]
	projection, err := store.Project(ProjectionRequest{Phase: PhaseTriage, Include: []ID{unknownID}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.Commit(CommitRequest{
		Phase:      PhaseTriage,
		Producer:   Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-2"},
		Projection: projection,
		Delta: Delta{
			Observations: []ObservationInput{{LocalRef: "o1", Content: "provider retained the instructions"}},
			Relations:    []RelationInput{{LocalRef: "resolve1", Kind: RelationResolves, Source: "o1", Target: string(unknownID)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.State().Unknowns[0].Status; got != UnknownResolved {
		t.Fatalf("status after resolution = %s", got)
	}
	projection, err = store.Project(ProjectionRequest{Phase: PhaseTriage, Include: []ID{unknownID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(CommitRequest{
		Phase:      PhaseTriage,
		Producer:   Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-3"},
		Projection: projection,
		Delta:      Delta{StatusChanges: []StatusChange{{Kind: ObjectUnknown, Ref: string(unknownID), Status: string(UnknownReopened)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	projection, err = store.Project(ProjectionRequest{Phase: PhaseTriage, Include: []ID{unknownID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(CommitRequest{
		Phase:      PhaseTriage,
		Producer:   Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-4"},
		Projection: projection,
		Delta: Delta{
			Observations: []ObservationInput{{LocalRef: "o2", Content: "provider preserves instructions on rerun"}},
			Relations:    []RelationInput{{LocalRef: "resolve2", Kind: RelationResolves, Source: "o2", Target: string(unknownID)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.State().Unknowns[0].Status; got != UnknownResolved {
		t.Fatalf("status after second resolution = %s", got)
	}

	before := len(store.Events())
	projection, err = store.Project(ProjectionRequest{Phase: PhaseTriage, Include: []ID{unknownID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(CommitRequest{
		Phase:      PhaseTriage,
		Producer:   Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-5"},
		Projection: projection,
		Delta:      Delta{Retractions: []RetractionInput{{Relation: string(resolved.IDs["resolve1"])}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := store.State()
	if len(store.Events()) <= before || len(state.Relations) != 2 || state.Relations[0].Status != RelationRetracted || state.Relations[1].Status != RelationActive {
		t.Fatalf("retraction removed history or failed: events=%d relations=%#v", len(store.Events()), state.Relations)
	}
	replayed, err := Replay(store.Events())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, replayed.State()) {
		t.Fatal("replay changed the final state")
	}
}
