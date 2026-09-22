package epistemic

import "testing"

func TestProjectionClosesOverCounterevidenceAndOperatorViewSummarisesState(t *testing.T) {
	store, err := NewStore("restore prompt delivery")
	if err != nil {
		t.Fatal(err)
	}
	triage, err := store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    Delta{Observations: []ObservationInput{{LocalRef: "o1", Content: "runtime received instructions"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseAbduce, Reason: "triage is complete"}); err != nil {
		t.Fatal(err)
	}
	abduceProjection, err := store.Project(ProjectionRequest{Phase: PhaseAbduce, Include: []ID{triage.IDs["o1"]}})
	if err != nil {
		t.Fatal(err)
	}
	abduction, err := store.Commit(CommitRequest{
		Phase:      PhaseAbduce,
		Producer:   Producer{Phase: PhaseAbduce, Process: "abducer", Artifact: "abduce-1"},
		Projection: abduceProjection,
		Delta: Delta{
			Hypotheses:        []HypothesisInput{{LocalRef: "h1", Mechanism: "the runtime loses the prompt", Falsifier: "provider capture lacks instructions"}},
			Relations:         []RelationInput{{LocalRef: "supports", Kind: RelationSupports, Source: string(triage.IDs["o1"]), Target: "h1"}},
			LeadingHypothesis: "h1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hypothesisID := abduction.IDs["h1"]
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseFrame, Reason: "a leading hypothesis is available"}); err != nil {
		t.Fatal(err)
	}
	frameProjection, err := store.Project(ProjectionRequest{Phase: PhaseFrame})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := store.Commit(CommitRequest{
		Phase:      PhaseFrame,
		Producer:   Producer{Phase: PhaseFrame, Process: "framer", Artifact: "frame-1"},
		Projection: frameProjection,
		Delta: Delta{
			Frames:      []FrameInput{{LocalRef: "f1", Name: "provider prompt flow", Summary: "verify prompt delivery across runtime boundaries"}},
			Relations:   []RelationInput{{LocalRef: "depends", Kind: RelationDependsOn, Source: "f1", Target: string(hypothesisID)}},
			ActiveFrame: "f1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	frameID := frame.IDs["f1"]
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseExecute, Reason: "frame is ready"}); err != nil {
		t.Fatal(err)
	}
	executeProjection, err := store.Project(ProjectionRequest{Phase: PhaseExecute})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(CommitRequest{
		Phase:      PhaseExecute,
		Producer:   Producer{Phase: PhaseExecute, Process: "executor", Artifact: "execute-1"},
		Projection: executeProjection,
		Delta: Delta{
			Observations: []ObservationInput{{LocalRef: "o2", Content: "provider capture did not contain developer instructions"}},
			Relations:    []RelationInput{{LocalRef: "contradicts", Kind: RelationContradicts, Source: "o2", Target: string(hypothesisID)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseAbduce, Reason: "provider capture contradicts the leading hypothesis", TargetID: hypothesisID, TargetKind: ObjectHypothesis}); err != nil {
		t.Fatal(err)
	}

	closed, err := store.Project(ProjectionRequest{Phase: PhaseAbduce, Include: []ID{hypothesisID}})
	if err != nil {
		t.Fatal(err)
	}
	if !closed.Contains(triage.IDs["o1"]) || !closed.Contains(store.State().Observations[1].ID) {
		t.Fatalf("projection omitted evidence or counterevidence: %#v", closed.IDs)
	}
	if len(closed.State.Relations) != 2 {
		t.Fatalf("projected relations = %d, want support and contradiction", len(closed.State.Relations))
	}
	view := store.OperatorView()
	if view.LeadingHypothesis == nil || view.LeadingHypothesis.ID != hypothesisID || view.ActiveFrame == nil || view.ActiveFrame.ID != frameID || view.ContradictionCount != 1 {
		t.Fatalf("operator view = %#v", view)
	}
}

func TestReplacementFrameRemainsVisibleAfterContradictionReopen(t *testing.T) {
	store, err := NewStore("preserve the active frame after a reframe")
	if err != nil {
		t.Fatal(err)
	}
	triage, err := store.Commit(CommitRequest{
		Phase:    PhaseTriage,
		Producer: Producer{Phase: PhaseTriage, Process: "triage", Artifact: "triage-1"},
		Delta:    Delta{Observations: []ObservationInput{{LocalRef: "o1", Content: "initial evidence"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseAbduce, Reason: "triage is complete"}); err != nil {
		t.Fatal(err)
	}
	abduceProjection, err := store.Project(ProjectionRequest{Phase: PhaseAbduce, Include: []ID{triage.IDs["o1"]}})
	if err != nil {
		t.Fatal(err)
	}
	abduction, err := store.Commit(CommitRequest{
		Phase:      PhaseAbduce,
		Producer:   Producer{Phase: PhaseAbduce, Process: "abducer", Artifact: "abduce-1"},
		Projection: abduceProjection,
		Delta: Delta{
			Hypotheses:        []HypothesisInput{{LocalRef: "h1", Mechanism: "the initial frame is valid"}},
			Relations:         []RelationInput{{LocalRef: "supports", Kind: RelationSupports, Source: string(triage.IDs["o1"]), Target: "h1"}},
			LeadingHypothesis: "h1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseFrame, Reason: "a leading hypothesis is available"}); err != nil {
		t.Fatal(err)
	}
	frameProjection, err := store.Project(ProjectionRequest{Phase: PhaseFrame})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.Commit(CommitRequest{
		Phase:      PhaseFrame,
		Producer:   Producer{Phase: PhaseFrame, Process: "framer", Artifact: "frame-1"},
		Projection: frameProjection,
		Delta: Delta{
			Frames:      []FrameInput{{LocalRef: "f1", Name: "initial frame", Summary: "the initial frame"}},
			Relations:   []RelationInput{{LocalRef: "depends", Kind: RelationDependsOn, Source: "f1", Target: string(abduction.IDs["h1"])}},
			ActiveFrame: "f1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	initialID := initial.IDs["f1"]
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseExecute, Reason: "frame is ready"}); err != nil {
		t.Fatal(err)
	}
	executeProjection, err := store.Project(ProjectionRequest{Phase: PhaseExecute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(CommitRequest{
		Phase:      PhaseExecute,
		Producer:   Producer{Phase: PhaseExecute, Process: "executor", Artifact: "execute-1"},
		Projection: executeProjection,
		Delta: Delta{
			Observations: []ObservationInput{{LocalRef: "o2", Content: "the initial frame is contradicted"}},
			Relations:    []RelationInput{{LocalRef: "contradicts", Kind: RelationContradicts, Source: "o2", Target: string(initialID)}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseFrame, Reason: "initial frame is contradicted", TargetID: initialID, TargetKind: ObjectFrame}); err != nil {
		t.Fatal(err)
	}
	reframeProjection, err := store.Project(ProjectionRequest{Phase: PhaseFrame})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Commit(CommitRequest{
		Phase:      PhaseFrame,
		Producer:   Producer{Phase: PhaseFrame, Process: "framer", Artifact: "frame-2"},
		Projection: reframeProjection,
		Delta: Delta{
			Frames:      []FrameInput{{LocalRef: "f2", Name: "replacement frame", Summary: "the replacement frame"}},
			Relations:   []RelationInput{{LocalRef: "supersedes", Kind: RelationSupersedes, Source: "f2", Target: string(initialID)}},
			ActiveFrame: "f2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	replacementID := replacement.IDs["f2"]
	if _, err := store.TransitionPhase(TransitionRequest{To: PhaseExecute, Reason: "replacement frame is ready"}); err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(ProjectionRequest{Phase: PhaseExecute})
	if err != nil {
		t.Fatal(err)
	}
	if store.State().Task.ActiveFrame != replacementID {
		t.Fatalf("active frame = %q, want replacement %q", store.State().Task.ActiveFrame, replacementID)
	}
	if len(projection.State.Frames) != 1 || projection.State.Frames[0].ID != replacementID {
		t.Fatalf("projected frames = %#v, want only replacement %q", projection.State.Frames, replacementID)
	}
	if !projection.Contains(replacementID) || projection.Contains(initialID) {
		t.Fatalf("projection IDs = %#v, want replacement and not superseded frame", projection.IDs)
	}
}
