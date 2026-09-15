package cognition

import (
	"testing"

	"github.com/haha-systems/ghost/internal/epistemic"
)

func TestPhaseArtifactParserRejectsModelRouting(t *testing.T) {
	_, err := ParsePhaseArtifact(epistemic.PhaseTriage, []byte(`{"classification":"prompt delivery","next_investigation":"capture provider request","route_to":"execute"}`))
	if err == nil {
		t.Fatal("phase artifact accepted a model-selected route")
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

func TestPhaseArtifactParserRejectsTrailingJSON(t *testing.T) {
	_, err := ParsePhaseArtifact(epistemic.PhaseFrame, []byte(`{"frame":{"local_ref":"f1","name":"test","summary":"verify"}} {}`))
	if err == nil {
		t.Fatal("parser accepted trailing JSON")
	}
}
