package app

import (
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
)

func TestTraceWriteFailureStopsWork(t *testing.T) {
	for _, path := range []string{"record", "CES"} {
		t.Run(path, func(t *testing.T) {
			m, _ := tracedCESModel(t, cesRunArtifacts...)
			m, _ = updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "trace failure"})
			if err := m.trace.Close(); err != nil {
				t.Fatal(err)
			}
			if path == "record" {
				m.record("", event.Event{Source: "test", Message: "write"}, nil)
			} else {
				m.traceCES(m.newCESRun(epistemic.PhaseTriage, "wraith"), "test", "write", "", "", nil)
			}
			if m.traceError == nil || m.cesActive() {
				t.Fatal("trace failure left work active")
			}
			if m.cesStore.State().Task.Status != epistemic.WorkIncomplete {
				t.Fatal("trace failure did not mark work incomplete")
			}
			found := false
			for _, e := range m.events {
				found = found || strings.Contains(e.Message, "trace write failed")
			}
			if !found {
				t.Fatal("trace failure warning is missing")
			}
		})
	}
}

func TestObserverTraceFailureRejectsPhaseResult(t *testing.T) {
	m, _ := tracedCESModel(t, cesRunArtifacts...)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "observer failure"})
	if cmd == nil {
		t.Fatal("phase was not scheduled")
	}
	if err := m.trace.Close(); err != nil {
		t.Fatal(err)
	}
	msg, ok := cmd().(cesPhaseResultMsg)
	if !ok || msg.err == nil {
		t.Fatalf("phase result = %#v", msg)
	}
	m, next := m.handlePhaseResult(msg)
	if next != nil || m.cesStore.State().Task.Status != epistemic.WorkIncomplete {
		t.Fatal("failed observer scheduled more work")
	}
	if len(m.cesStore.State().Observations) != 0 {
		t.Fatal("failed trace committed an artifact")
	}
}

func TestFailedTerminalTransitionMarksRunIncomplete(t *testing.T) {
	store, err := epistemic.NewStore("terminal failure")
	if err != nil {
		t.Fatal(err)
	}
	controller := cesController{store: store}
	terminal := controller.terminal(epistemic.WorkComplete, "", "")
	if terminal.status != epistemic.WorkIncomplete || !strings.Contains(terminal.detail, "set terminal work status") {
		t.Fatalf("terminal = %#v", terminal)
	}
	if store.State().Task.Status != epistemic.WorkIncomplete || store.State().Task.TerminalReason != "phase_execution_failed" {
		t.Fatalf("task = %#v", store.State().Task)
	}
}

func TestFinalTraceFailureOverridesTerminalStatus(t *testing.T) {
	m, _ := tracedCESModel(t, cesRunArtifacts...)
	m, _ = updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "final trace failure"})
	if _, err := m.cesStore.SetTerminal(epistemic.WorkIncomplete, "underdetermined"); err != nil {
		t.Fatal(err)
	}
	if err := m.trace.Close(); err != nil {
		t.Fatal(err)
	}
	m.traceCES(m.newCESRun(epistemic.PhaseTriage, "wraith"), "final", "write", "", "", nil)
	if task := m.cesStore.State().Task; task.Status != epistemic.WorkIncomplete || task.TerminalReason != "phase_execution_failed" {
		t.Fatalf("task = %#v", task)
	}
	replay, err := epistemic.Replay(m.cesStore.Events())
	if err != nil {
		t.Fatal(err)
	}
	if replay.State().Task.TerminalReason != "phase_execution_failed" {
		t.Fatal("replay lost run failure")
	}
}
