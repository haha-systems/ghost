package core

import (
	"encoding/json"
	"testing"
	"time"
)

func decisionRequest() DecisionRequest {
	return DecisionRequest{
		ID: "event_00000000000000010000000000000001", Kind: DecisionKindCESMode,
		TaskID: "task_00000000000000010000000000000001", RequestingAgent: "agent_00000000000000010000000000000001",
		State: json.RawMessage(`{"goal":"inspect"}`), Actions: []string{"ces_on", "ces_off"},
		Budget: DecisionBudget{Timeout: time.Second},
	}
}

func TestDecisionRequestValidate(t *testing.T) {
	valid := decisionRequest()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	tests := []struct {
		name string
		edit func(*DecisionRequest)
	}{
		{"missing id", func(r *DecisionRequest) { r.ID = "" }},
		{"unknown kind", func(r *DecisionRequest) { r.Kind = "other" }},
		{"invalid state", func(r *DecisionRequest) { r.State = json.RawMessage("{") }},
		{"no actions", func(r *DecisionRequest) { r.Actions = nil }},
		{"empty action", func(r *DecisionRequest) { r.Actions = []string{" "} }},
		{"negative timeout", func(r *DecisionRequest) { r.Budget.Timeout = -time.Second }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := decisionRequest()
			test.edit(&r)
			if err := r.Validate(); err == nil {
				t.Fatal("Validate succeeded")
			}
		})
	}
}

func TestDecisionResponseValidate(t *testing.T) {
	request := decisionRequest()
	response := DecisionResponse{RequestID: request.ID, Action: "ces_on", Uncertainty: .25, ModelOutput: json.RawMessage(`{"choice":"ces_on"}`)}
	if err := response.Validate(request); err != nil {
		t.Fatalf("valid response: %v", err)
	}
	for name, edit := range map[string]func(*DecisionResponse){
		"wrong request":    func(r *DecisionResponse) { r.RequestID = "event_00000000000000010000000000000002" },
		"unoffered action": func(r *DecisionResponse) { r.Action = "other" },
		"uncertainty":      func(r *DecisionResponse) { r.Uncertainty = 1.1 },
		"invalid output":   func(r *DecisionResponse) { r.ModelOutput = json.RawMessage("{") },
	} {
		t.Run(name, func(t *testing.T) {
			value := response
			edit(&value)
			if err := value.Validate(request); err == nil {
				t.Fatal("Validate succeeded")
			}
		})
	}
}

func TestCapabilityValidation(t *testing.T) {
	for _, capability := range []Capability{CapabilityCodeExecution, CapabilityReview, CapabilityConsultation, CapabilityClassification, CapabilityDecisionSupport, CapabilityMemoryAccess, CapabilityDelegation} {
		if err := capability.Validate(); err != nil {
			t.Errorf("%q: %v", capability, err)
		}
	}
	if err := Capability("shell").Validate(); err == nil {
		t.Fatal("unknown capability succeeded")
	}
	if err := validAgent().Validate(); err != nil {
		t.Fatalf("agent with empty capabilities: %v", err)
	}
}
