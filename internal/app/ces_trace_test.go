package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/haha-systems/ghost/internal/cognition"
	ghosttrace "github.com/haha-systems/ghost/internal/trace"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
)

type traceLine struct {
	Source    string            `json:"source"`
	Kind      string            `json:"kind"`
	Message   string            `json:"message"`
	SessionID string            `json:"session_id"`
	TurnID    string            `json:"turn_id"`
	Meta      map[string]string `json:"meta"`
}

func tracedCESModel(t *testing.T, artifacts ...string) (Model, string) {
	t.Helper()
	m, _, _ := cesModel(t, artifacts...)
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	w, err := ghosttrace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	m.trace = w
	return m, path
}

func readTrace(t *testing.T, path string) []traceLine {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []traceLine
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line traceLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("invalid trace line %q: %v", scanner.Text(), err)
		}
		lines = append(lines, line)
	}
	return lines
}

// phaseRun returns the trace records of the first run of phase, in order.
func phaseRun(lines []traceLine, phase string) (string, []traceLine) {
	runID := ""
	var out []traceLine
	for _, line := range lines {
		id := line.Meta["phase_run_id"]
		if id == "" || line.Meta["ces_event"] == "" || line.Meta["phase"] != phase {
			continue
		}
		if runID == "" {
			runID = id
		}
		if id == runID {
			out = append(out, line)
		}
	}
	return runID, out
}

// A successful phase exposes the whole chain, from scheduling to transition,
// under one phase_run_id, with session and turn ids once they are known.
func TestTraceRecordsCompletePhaseChain(t *testing.T) {
	m, path := tracedCESModel(t, cesRunArtifacts[:2]...)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	m, _ = runCmd(t, m, cmd)
	_ = m

	lines := readTrace(t, path)
	runID, triage := phaseRun(lines, "triage")
	if runID == "" {
		t.Fatalf("no triage phase run in trace: %+v", lines)
	}
	want := []string{
		cognition.PhaseResourceSelected,
		cognition.PhaseScheduled,
		cognition.PhaseSessionStarting,
		cognition.PhaseSessionStarted,
		cognition.PhaseTurnRequested,
		cognition.PhaseTurnStarted,
		cognition.PhaseActivity,
		cognition.PhaseTurnCompleted,
		cognition.PhaseArtifactReceived,
		cognition.PhaseArtifactParsed,
		cognition.PhaseFinished,
		cognition.PhaseDeltaGenerated,
		cognition.PhaseCommitSucceeded,
		cognition.PhaseTransition,
	}
	got := []string{}
	for _, line := range triage {
		got = append(got, line.Meta["ces_event"])
	}
	next := 0
	for _, name := range got {
		if next < len(want) && name == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("triage chain = %v, missing %q (want in order %v)", got, want[next], want)
	}

	sessionID := ""
	for _, line := range triage {
		for _, key := range []string{"work_id", "phase", "resource_id", "agent_id", "phase_run_id"} {
			if line.Meta[key] == "" {
				t.Fatalf("%s record lacks %s: %+v", line.Meta["ces_event"], key, line)
			}
		}
		switch line.Meta["ces_event"] {
		case cognition.PhaseSessionStarted:
			sessionID = line.SessionID
		case cognition.PhaseTurnCompleted, cognition.PhaseCommitSucceeded:
			if line.SessionID == "" || line.SessionID != sessionID || line.TurnID != "turn-1" {
				t.Fatalf("%s correlation = session %q turn %q, want %q/turn-1", line.Meta["ces_event"], line.SessionID, line.TurnID, sessionID)
			}
		case cognition.PhaseActivity:
			if line.Source != "WRAITH" || line.Meta["elapsed_ms"] == "" {
				t.Fatalf("activity record = %+v", line)
			}
		case cognition.PhaseResourceSelected:
			if line.Meta["qac_to"] != "wraith" || line.Meta["requested_phase"] != "triage" {
				t.Fatalf("qac selection record = %+v", line)
			}
		case cognition.PhaseTransition:
			if line.Meta["from_phase"] != "triage" || line.Meta["to_phase"] != "abduce" || line.Meta["reason"] == "" {
				t.Fatalf("transition record = %+v", line)
			}
		}
	}
	if sessionID == "" {
		t.Fatal("session id never recorded")
	}
	for _, line := range triage {
		if line.Meta["ces_event"] == cognition.PhaseCommitSucceeded && line.Meta["revision_id"] == "" {
			t.Fatalf("commit record lacks revision: %+v", line)
		}
	}

	abduceID, abduce := phaseRun(lines, "abduce")
	if abduceID == "" || abduceID == runID || len(abduce) == 0 {
		t.Fatalf("abduce run id = %q (triage %q)", abduceID, runID)
	}
}

// A malformed artifact is distinguishable from a turn that never completed.
func TestTraceSeparatesMalformedArtifactFromMissingCompletion(t *testing.T) {
	m, path := tracedCESModel(t, `{"classification":"defect","unexpected":true}`)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	_, _ = runCmd(t, m, cmd)

	_, triage := phaseRun(readTrace(t, path), "triage")
	seen := map[string]traceLine{}
	for _, line := range triage {
		seen[line.Meta["ces_event"]] = line
	}
	if _, ok := seen[cognition.PhaseTurnCompleted]; !ok {
		t.Fatalf("turn completion not recorded: %v", seen)
	}
	_, invalid := seen[cognition.PhaseArtifactInvalid]
	_, deltaFailed := seen[cognition.PhaseDeltaFailed]
	if !invalid && !deltaFailed {
		t.Fatalf("malformed artifact not recorded: %v", seen)
	}
	if _, ok := seen[cognition.PhaseCommitSucceeded]; ok {
		t.Fatal("a malformed artifact was recorded as committed")
	}
	if finished := seen[cognition.PhaseFinished]; invalid && finished.Meta["outcome"] != "error" {
		t.Fatalf("finish record = %+v", finished)
	}
}
