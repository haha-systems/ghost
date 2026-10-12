package run

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

type fixedProgressVerifier cognition.ProgressEvidence

func (v fixedProgressVerifier) VerifyProgress(_ epistemic.State, _ cognition.PhaseArtifact) cognition.ProgressEvidence {
	return cognition.ProgressEvidence(v)
}

type appJev struct{ calls int }

func (j *appJev) Assess(_ context.Context, _ cognition.JevInput) ([]byte, error) {
	j.calls++
	return []byte(`{"current_resource_adequacy":0.1,"expected_gain_from_stronger_cognition":0.9,"decomposition_need":0.1,"confidence":0.9,"judgment":"escalate_cognition"}`), nil
}

func TestArtifactRepairExhaustionHasExplicitReasonForStructuralFailure(t *testing.T) {
	store, err := epistemic.NewStore("classify exhausted artifact repair")
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := cognition.New(appQACConfig().QAC)
	if err != nil {
		t.Fatal(err)
	}
	phase := epistemic.PhaseTriage
	repeats := 1
	controller := cesController{
		store:        store,
		orchestrator: cognition.NewOrchestrator(store),
		coordinator:  coordinator,
		phase:        &phase,
		repeats:      &repeats,
	}
	exhausted := &cognition.ArtifactRepairExhaustedError{
		Err:    errors.New("artifact repair exhausted after 2 attempts: decode phase artifact"),
		Commit: false,
	}
	outcome := controller.process(context.Background(), cesPhaseResultMsg{
		phase: epistemic.PhaseTriage,
		run:   cesRun{id: "run-structural", workID: string(store.State().Task.ID)},
		err:   exhausted,
	}, cesControllerHookFuncs{
		recordFn: func(string, event.Event, map[string]string) {},
	})
	if outcome.terminal == nil || outcome.terminal.reason != "artifact_repair_exhausted" {
		t.Fatalf("terminal outcome = %#v, want explicit artifact repair exhaustion", outcome.terminal)
	}
	if store.State().Task.Status != epistemic.WorkIncomplete || store.State().Task.TerminalReason != "artifact_repair_exhausted" {
		t.Fatalf("task = %#v, want incomplete artifact_repair_exhausted", store.State().Task)
	}
}

func TestCognitiveSituationKeepsAcceptedArtifactsSeparateFromVerifiedProgress(t *testing.T) {
	store, err := epistemic.NewStore("Ghostdive integration")
	if err != nil {
		t.Fatal(err)
	}
	repeats := 3
	controller := cesController{
		store: store, repeats: &repeats,
		progress: fixedProgressVerifier{State: cognition.ProgressLow, Provenance: cognition.ProgressStructuredExecution, Strength: 1},
	}
	artifact, err := cognition.ParsePhaseArtifact(epistemic.PhaseExecute, []byte(`{
		"actions":[{"local_ref":"a1","description":"change"}],
		"reported_progress":{"state":"meaningful"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	situation := controller.cognitiveSituation(epistemic.PhaseExecute, cognition.QACRequest{}, artifact)
	if !situation.ArtifactAccepted || situation.Progress.State != cognition.ProgressLow || situation.Progress.Provenance != cognition.ProgressStructuredExecution {
		t.Fatalf("situation=%#v", situation)
	}
	if situation.ReportedProgress == nil || situation.ReportedProgress.State != cognition.ProgressMeaningful {
		t.Fatalf("reported progress=%#v", situation.ReportedProgress)
	}
}

func TestRepeatedExecuteWithLowEvidenceCallsJevAtAllocationBoundary(t *testing.T) {
	store, err := epistemic.NewStore("Ghostdive integration")
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := cognition.NewOrchestrator(store)
	for range 3 {
		if _, err := orchestrator.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	coordinator, err := cognition.New(appQACConfig().QAC)
	if err != nil {
		t.Fatal(err)
	}
	start, err := coordinator.StartWork("Ghostdive integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Commit(start, time.Now()); err != nil {
		t.Fatal(err)
	}
	jev := &appJev{}
	coordinator.SetJev(jev)
	phase, repeats := epistemic.PhaseExecute, 1
	controller := cesController{
		store: store, orchestrator: orchestrator, coordinator: coordinator,
		sessions: map[string]runtime.Session{"wraith": fake.NewSession("wraith"), "shade": fake.NewSession("shade")}, phase: &phase, repeats: &repeats,
		progress: fixedProgressVerifier{State: cognition.ProgressLow, Provenance: cognition.ProgressStructuredExecution, Strength: 1},
	}
	artifact, err := cognition.ParsePhaseArtifact(epistemic.PhaseExecute, []byte(`{
		"observations":[{"local_ref":"o1","content":"the integration remains blocked"}],
		"actions":[{"local_ref":"a1","description":"try the integration"}],
		"relations":[{"local_ref":"r1","kind":"contradicts","source":"o1","target":"a1"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	noTrace := func(cesRun, string, string, string, string, map[string]string) {}
	outcome := controller.process(context.Background(), cesPhaseResultMsg{
		phase: epistemic.PhaseExecute, resource: "wraith", run: cesRun{id: "run", workID: string(store.State().Task.ID)}, result: cognition.PhaseResult{Artifact: artifact},
	}, cesControllerHookFuncs{
		newRunFn: func(phase epistemic.Phase, resource string) cesRun {
			return cesRun{id: "next", phase: phase, resource: resource}
		},
		traceFn: noTrace, traceQACSelectionFn: func(cesRun, cognition.Plan) {}, traceTransitionFn: func(cesRun, epistemic.Phase) {},
		recordFn: func(string, event.Event, map[string]string) {}, publishFn: func() {}, qacMetaFn: func(cognition.Plan) map[string]string { return nil },
	})
	if jev.calls != 1 || outcome.plan == nil || outcome.plan.Jev == nil {
		t.Fatalf("calls=%d outcome=%#v", jev.calls, outcome)
	}
	if store.State().Task.Phase != epistemic.PhaseExecute {
		t.Fatalf("CES state was not preserved through allocation: %#v", store.State().Task)
	}
}
