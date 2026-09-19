package cognition

import (
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/epistemic"
)

func TestPhaseArtifactParserRejectsModelRouting(t *testing.T) {
	_, err := ParsePhaseArtifact(epistemic.PhaseTriage, []byte(`{"classification":"prompt delivery","next_investigation":"capture provider request","route_to":"execute"}`))
	if err == nil {
		t.Fatal("phase artifact accepted a model-selected route")
	}
}

func TestPhaseArtifactValidationRejectsInvalidImplicitAndExplicitRelations(t *testing.T) {
	cases := []struct {
		name       string
		phase      epistemic.Phase
		input      string
		projection epistemic.Projection
	}{
		{
			name:  "triage claim evidence is unknown",
			phase: epistemic.PhaseTriage,
			input: `{"classification":"bug","claims":[{"local_ref":"c1","text":"claim","evidence_ref":"ces_unknown"}],"next_investigation":"inspect"}`,
			projection: epistemic.Projection{Phase: epistemic.PhaseTriage, State: epistemic.State{
				Unknowns: []epistemic.Unknown{{ID: "ces_unknown", Question: "what happened?"}},
			}},
		},
		{
			name:  "frame supersedes hypothesis",
			phase: epistemic.PhaseFrame,
			input: `{"frame":{"local_ref":"f1","name":"fix","summary":"fix it"},"supersedes_frame_ref":"ces_hypothesis"}`,
			projection: epistemic.Projection{Phase: epistemic.PhaseFrame, State: epistemic.State{
				Hypotheses: []epistemic.Hypothesis{{ID: "ces_hypothesis", Mechanism: "cause"}},
			}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			artifact, err := ParsePhaseArtifact(test.phase, []byte(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if err := artifact.Validate(test.projection); err == nil || !strings.Contains(err.Error(), "cannot connect") {
				t.Fatalf("validation error = %v, want invalid relation endpoints", err)
			}
		})
	}
}

func TestExecutionArtifactRejectsUnknownResolutionField(t *testing.T) {
	_, err := ParsePhaseArtifact(epistemic.PhaseExecute, []byte(`{
		"actions":[{"local_ref":"a1","description":"edit"}],
		"resolved_unknowns":[{"local_ref":"r1","kind":"resolves","source":"o1","target":"ces_unknown"}]
	}`))
	if err == nil {
		t.Fatal("EXECUTE accepted unknown resolution output")
	}
}

func TestClosureArtifactValidationAcceptsLocalVerificationObservation(t *testing.T) {
	artifact, err := ParsePhaseArtifact(epistemic.PhaseClose, []byte(`{
		"observations":[{"local_ref":"o1","content":"tests passed"}],
		"relations":[{"local_ref":"r1","kind":"tests","source":"o1","target":"ces_action"}],
		"completion_recommended":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	projection := epistemic.Projection{Phase: epistemic.PhaseClose, State: epistemic.State{
		Actions: []epistemic.Action{{ID: "ces_action", Description: "change"}},
	}}
	if err := artifact.Validate(projection); err != nil {
		t.Fatal(err)
	}
}

func TestClosureArtifactValidationAcceptsEvidenceResolution(t *testing.T) {
	artifact, err := ParsePhaseArtifact(epistemic.PhaseClose, []byte(`{
		"observations":[{"local_ref":"o1","content":"the check answered the open question"}],
		"resolved_unknowns":[{"local_ref":"r1","kind":"resolves","source":"o1","target":"ces_unknown"}],
		"completion_recommended":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := artifact.Validate(epistemic.Projection{Phase: epistemic.PhaseClose, State: epistemic.State{
		Unknowns: []epistemic.Unknown{{ID: "ces_unknown", Question: "what happened?"}},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestClosureArtifactRejectsOutcomeResolution(t *testing.T) {
	artifact, err := ParsePhaseArtifact(epistemic.PhaseClose, []byte(`{
		"resolved_unknowns":[{"local_ref":"r1","kind":"resolves","source":"ces_outcome","target":"ces_unknown"}],
		"completion_recommended":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := artifact.Validate(epistemic.Projection{Phase: epistemic.PhaseClose, State: epistemic.State{
		Outcomes: []epistemic.Outcome{{ID: "ces_outcome", Description: "result"}},
		Unknowns: []epistemic.Unknown{{ID: "ces_unknown", Question: "what happened?"}},
	}}); err == nil || !strings.Contains(err.Error(), "cannot connect") {
		t.Fatalf("validation error = %v, want invalid relation endpoints", err)
	}
}

func TestFrameValidationExplainsDependsOnEndpointKinds(t *testing.T) {
	artifact, err := ParsePhaseArtifact(epistemic.PhaseFrame, []byte(`{
		"frame":{"local_ref":"f1","name":"fix","summary":"fix it"},
		"relations":[{"local_ref":"r1","kind":"depends_on","source":"f1","target":"ces_observation"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	err = artifact.Validate(epistemic.Projection{Phase: epistemic.PhaseFrame, State: epistemic.State{
		Observations: []epistemic.Observation{{ID: "ces_observation", Content: "evidence"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "allowed targets: hypothesis, constraint, frame") {
		t.Fatalf("validation error = %v, want allowed endpoint kinds", err)
	}
}

func TestAbductionArtifactConvertsToNarrowStoreDelta(t *testing.T) {
	artifact, err := ParsePhaseArtifact(epistemic.PhaseAbduce, []byte(`{
		"hypotheses":[{"local_ref":"h1","mechanism":"runtime drops instructions","falsifier":"provider request lacks instructions"}],
		"relations":[{"local_ref":"r1","kind":"supports","source":"ces_evidence","target":"h1"}],
		"leading_hypothesis_ref":"h1",
		"remaining_uncertainty":"provider capture required"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	delta, err := artifact.Delta()
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Hypotheses) != 1 || delta.Hypotheses[0].LocalRef != "h1" || len(delta.Relations) != 1 || delta.LeadingHypothesis != "h1" {
		t.Fatalf("delta = %#v", delta)
	}
	if delta.WorkStatus != nil || delta.TerminalReason != "" {
		t.Fatal("phase artifact can control terminal status")
	}
}

func TestAbductionArtifactRejectsResolvedUnknowns(t *testing.T) {
	_, err := ParsePhaseArtifact(epistemic.PhaseAbduce, []byte(`{
		"hypotheses":[{"local_ref":"h1","mechanism":"runtime drops instructions","falsifier":"provider request lacks instructions"}],
		"leading_hypothesis_ref":"h1",
		"resolved_unknowns":[{"local_ref":"r1","kind":"resolves","source":"ces_unknown","target":"ces_unknown"}]
	}`))
	if err == nil {
		t.Fatal("ABDUCE accepted unknown resolution without new evidence")
	}
}

func TestArtifactsRejectIgnoredObservationReferenceFields(t *testing.T) {
	tests := []struct {
		phase epistemic.Phase
		input string
	}{
		{epistemic.PhaseExecute, `{"actions":[{"local_ref":"a1","description":"edit"}],"observation_refs":["ces_observation"]}`},
		{epistemic.PhaseClose, `{"verification_observation_refs":["ces_observation"],"completion_recommended":true}`},
	}
	for _, test := range tests {
		if _, err := ParsePhaseArtifact(test.phase, []byte(test.input)); err == nil {
			t.Fatalf("%s accepted an ignored observation-reference field", test.phase)
		}
	}
}

func TestPhaseArtifactParserRejectsTrailingJSON(t *testing.T) {
	_, err := ParsePhaseArtifact(epistemic.PhaseFrame, []byte(`{"frame":{"local_ref":"f1","name":"test","summary":"verify"}} {}`))
	if err == nil {
		t.Fatal("parser accepted trailing JSON")
	}
}
