package decision

import (
	"context"
	"encoding/json"

	"github.com/haha-systems/ghost/internal/core"
)

// ConsultationRulesAgent selects the first candidate other than the parent.
// This fixed rule is a bounded heuristic, not a model judgment.
type ConsultationRulesAgent struct{}

func NewConsultationRulesAgent() *ConsultationRulesAgent { return &ConsultationRulesAgent{} }

func (*ConsultationRulesAgent) Info() Info {
	return Info{ID: "rules-consultation", Version: "1", Kinds: []core.DecisionKind{core.DecisionKindConsultation}, Capabilities: []core.Capability{core.CapabilityDecisionSupport}}
}

func (*ConsultationRulesAgent) Decide(_ context.Context, request core.DecisionRequest) (core.DecisionResponse, error) {
	if request.Kind != core.DecisionKindConsultation {
		return core.DecisionResponse{}, invalidOutput("consultation rules agent cannot answer kind %q", request.Kind)
	}
	var state struct {
		ParentAgent string `json:"parent_agent"`
		Candidates  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"candidate_agents"`
	}
	if err := json.Unmarshal(request.State, &state); err != nil {
		return core.DecisionResponse{}, invalidOutput("decode consultation decision state: %v", err)
	}
	action := "none"
	for _, candidate := range state.Candidates {
		if candidate.ID == "" || candidate.Name == "" || candidate.ID == state.ParentAgent || candidate.Name == state.ParentAgent {
			continue
		}
		candidateAction := "consult:" + candidate.Name
		if hasAction(request.Actions, candidateAction) {
			action = candidateAction
			break
		}
	}
	if !hasAction(request.Actions, action) {
		return core.DecisionResponse{}, invalidOutput("consultation action %q was not offered", action)
	}
	return core.DecisionResponse{
		RequestID: request.ID, Action: action, Uncertainty: .25,
		Supporting: map[string]string{"heuristic": "first candidate other than parent"},
		Meta:       core.DecisionMeta{AgentID: "rules-consultation", Version: "1"},
	}, nil
}

func hasAction(actions []string, wanted string) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}
