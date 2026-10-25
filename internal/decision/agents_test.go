package decision_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
)

type fixedJev struct{ raw []byte }

func (j fixedJev) Assess(context.Context, cognition.JevInput) ([]byte, error) { return j.raw, nil }

func TestJevAgentMapsStrictJudgment(t *testing.T) {
	raw := []byte(`{"current_resource_adequacy":0.4,"expected_gain_from_stronger_cognition":0.8,"decomposition_need":0.2,"confidence":0.75,"judgment":"escalate_cognition"}`)
	state, _ := json.Marshal(cognition.JevInput{Phase: "execute", ProgressState: cognition.ProgressLow})
	agent := decision.NewJevAgent(fixedJev{raw: raw})
	out, err := agent.Decide(context.Background(), core.DecisionRequest{ID: "event_00000000000000010000000000000001", Kind: core.DecisionKindCognitiveAdequacy, State: state, Actions: []string{"continue_current_resource", "escalate_cognition"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Action != "escalate_cognition" || out.Uncertainty != .25 || string(out.ModelOutput) != string(raw) || out.Supporting["current_resource_adequacy"] != "0.4" {
		t.Fatalf("response = %#v", out)
	}
}

func TestJevAgentRejectsInvalidOutput(t *testing.T) {
	state, _ := json.Marshal(cognition.JevInput{})
	_, err := decision.NewJevAgent(fixedJev{raw: []byte(`{"judgment":"unknown"}`)}).Decide(context.Background(), core.DecisionRequest{ID: "event_00000000000000010000000000000001", Kind: core.DecisionKindCognitiveAdequacy, State: state, Actions: []string{"unknown"}})
	if err == nil || !decision.IsInvalidOutput(err) {
		t.Fatalf("error = %v, want invalid output", err)
	}
}

func TestRulesAgentUsesDocumentedCESHeuristic(t *testing.T) {
	agent := decision.NewRulesAgent()
	for _, test := range []struct {
		goal string
		want string
	}{
		{"Fix the label", "ces_off"},
		{"Inspect the report, check the event path, and then compare replay output", "ces_on"},
		{"1. Inspect the event path\n2. Compare replay output", "ces_on"},
	} {
		state, _ := json.Marshal(map[string]string{"goal": test.goal})
		out, err := agent.Decide(context.Background(), core.DecisionRequest{ID: "event_00000000000000010000000000000001", Kind: core.DecisionKindCESMode, State: state, Actions: []string{"ces_on", "ces_off"}})
		if err != nil || out.Action != test.want {
			t.Errorf("goal %q: action=%q err=%v, want %q", test.goal, out.Action, err, test.want)
		}
	}
}

func TestServiceTurnsInvalidJevOutputIntoInvalidOutcome(t *testing.T) {
	state, _ := json.Marshal(cognition.JevInput{Phase: "execute"})
	service := decision.NewService(decision.ServiceConfig{Enabled: map[core.DecisionKind]bool{core.DecisionKindCognitiveAdequacy: true}})
	if err := service.Register(decision.NewJevAgent(fixedJev{raw: []byte(`{"judgment":"unknown"}`)})); err != nil {
		t.Fatal(err)
	}
	outcome := service.Decide(context.Background(), core.DecisionRequest{
		ID: "event_00000000000000010000000000000001", Kind: core.DecisionKindCognitiveAdequacy,
		TaskID: "task_00000000000000010000000000000001", RequestingAgent: "agent_00000000000000010000000000000001",
		State: state, Actions: []string{"continue_current_resource", "escalate_cognition"},
	})
	if outcome.Status != decision.StatusInvalid {
		t.Fatalf("outcome = %#v", outcome)
	}
}
