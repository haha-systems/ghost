package cognition

import "testing"

func TestParsePhaseArtifactRejectsInvalidQACRequest(t *testing.T) {
	_, err := ParsePhaseArtifact("triage", []byte(`{"classification":"defect","next_investigation":"check","qac_request":{"direction":"escalate","uncertainty":2}}`))
	if err == nil {
		t.Fatal("invalid qac request was accepted")
	}
}
