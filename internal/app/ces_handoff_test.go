package app

import (
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
)

// A TRIAGE artifact that asks QAC to release is advice about who runs ABDUCE.
// CES must commit and advance first, QAC then picks the resource, and ABDUCE
// runs in a fresh session: the direction never stops the work or picks a phase.
func TestReleaseRequestAdvancesToAbduceBeforeResourceSelection(t *testing.T) {
	release := `{"classification":"defect","observations":[{"local_ref":"o1","content":"the runner drops item/completed"}],"claims":[{"local_ref":"c1","text":"final messages are lost","evidence_ref":"o1"}],"next_investigation":"inspect the runner filter","qac_request":{"direction":"release","uncertainty":0,"novelty":0,"expected_gain":0,"reason":"a cheaper resource can abduce"}}`
	m, path := tracedCESModel(t, release, cesRunArtifacts[1])
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	m, cmd = runCmd(t, m, cmd)
	view, _ := m.OperatorView()
	if view.WorkStatus != epistemic.WorkActive || view.Phase != epistemic.PhaseAbduce {
		t.Fatalf("after release triage: status %q phase %q", view.WorkStatus, view.Phase)
	}
	if cmd == nil {
		t.Fatal("abduce was not scheduled")
	}
	m, _ = runCmd(t, m, cmd)
	started := m.phaseRuntime.(*scriptedRuntime).started()
	if len(started) != 2 || started[0].id == started[1].id || !strings.Contains(started[1].config.Instructions, "CES PHASE: ABDUCE") {
		t.Fatalf("abduce did not run in a fresh session: %d sessions", len(started))
	}
	if strings.Contains(started[1].input, started[0].id) {
		t.Fatalf("abduce input carries the triage session: %q", started[1].input)
	}

	order := []string{}
	for _, line := range readTrace(t, path) {
		name, phase := line.Meta["ces_event"], line.Meta["phase"]
		switch {
		case phase == "triage" && (name == cognition.PhaseArtifactReceived || name == cognition.PhaseArtifactParsed || name == cognition.PhaseDeltaGenerated || name == cognition.PhaseCommitSucceeded || name == cognition.PhaseTransition):
			if name == cognition.PhaseArtifactReceived && (line.Meta["source"] == "" || line.Meta["bytes"] == "") {
				t.Fatalf("artifact_received = %+v", line)
			}
			order = append(order, name)
		case phase == "abduce" && (name == cognition.PhaseQACRequest || name == cognition.PhaseResourceSelected || name == cognition.PhaseNextStarted):
			if name == cognition.PhaseQACRequest && line.Meta["direction"] != "release" {
				t.Fatalf("qac_request = %+v", line)
			}
			order = append(order, name)
		}
	}
	want := []string{
		cognition.PhaseArtifactReceived, cognition.PhaseArtifactParsed, cognition.PhaseDeltaGenerated,
		cognition.PhaseCommitSucceeded, cognition.PhaseTransition,
		cognition.PhaseQACRequest, cognition.PhaseResourceSelected, cognition.PhaseNextStarted,
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("boundary order = %v\nwant %v", order, want)
	}
}
