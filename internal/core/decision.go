package core

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// DecisionKind names a bounded question that may be sent to a decision agent.
type DecisionKind string

const (
	DecisionCESMode               DecisionKind = "ces_mode"
	DecisionCognitiveAdequacy     DecisionKind = "cognitive_adequacy"
	DecisionConsultation          DecisionKind = "consultation"
	DecisionKindCESMode                        = DecisionCESMode
	DecisionKindCognitiveAdequacy              = DecisionCognitiveAdequacy
	DecisionKindConsultation                   = DecisionConsultation
)

func (k DecisionKind) Validate() error {
	switch k {
	case DecisionKindCESMode, DecisionKindCognitiveAdequacy, DecisionKindConsultation:
		return nil
	default:
		return fmt.Errorf("unknown decision kind %q", k)
	}
}

// DecisionRequest contains only the state and actions needed for one decision.
type DecisionRequest struct {
	ID              EventID             `json:"id"`
	Kind            DecisionKind        `json:"kind"`
	TaskID          TaskID              `json:"task_id"`
	RequestingAgent AgentID             `json:"requesting_agent"`
	State           json.RawMessage     `json:"state,omitempty"`
	Actions         []string            `json:"actions"`
	Constraints     DecisionConstraints `json:"constraints,omitempty"`
	MemoryEvidence  []string            `json:"memory_evidence,omitempty"`
	Budget          DecisionBudget      `json:"budget,omitempty"`
}

type DecisionConstraints struct {
	Permissions    string `json:"permissions,omitempty"`
	ManualOverride string `json:"manual_override,omitempty"`
	Notes          string `json:"notes,omitempty"`
}

type DecisionBudget struct {
	Timeout time.Duration `json:"timeout,omitempty"`
}

func (r DecisionRequest) Validate() error {
	if err := r.ID.Validate(); err != nil {
		return fmt.Errorf("decision request ID: %w", err)
	}
	if err := r.Kind.Validate(); err != nil {
		return err
	}
	if err := r.TaskID.Validate(); err != nil {
		return fmt.Errorf("decision task ID: %w", err)
	}
	if err := r.RequestingAgent.Validate(); err != nil {
		return fmt.Errorf("decision requesting agent: %w", err)
	}
	if len(r.State) > 0 && !json.Valid(r.State) {
		return fmt.Errorf("decision state is not valid JSON")
	}
	if len(r.Actions) == 0 {
		return fmt.Errorf("decision actions are required")
	}
	if err := validateUniqueStrings("decision action", r.Actions); err != nil {
		return err
	}
	if r.Budget.Timeout < 0 {
		return fmt.Errorf("decision timeout must not be negative")
	}
	for _, ref := range r.MemoryEvidence {
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("decision memory evidence reference is empty")
		}
	}
	return nil
}

// DecisionResponse is an agent recommendation; Ghost remains responsible for execution.
type DecisionResponse struct {
	RequestID   EventID           `json:"request_id"`
	Action      string            `json:"action"`
	Supporting  map[string]string `json:"supporting,omitempty"`
	Uncertainty float64           `json:"uncertainty"`
	ModelOutput json.RawMessage   `json:"model_output,omitempty"`
	Meta        DecisionMeta      `json:"meta,omitempty"`
}

type DecisionMeta struct {
	AgentID  string        `json:"agent_id,omitempty"`
	Version  string        `json:"version,omitempty"`
	Model    string        `json:"model,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
}

func (r DecisionResponse) Validate(request DecisionRequest) error {
	if err := r.RequestID.Validate(); err != nil {
		return fmt.Errorf("decision response request ID: %w", err)
	}
	if r.RequestID != request.ID {
		return fmt.Errorf("decision response request ID %q does not match %q", r.RequestID, request.ID)
	}
	if strings.TrimSpace(r.Action) == "" {
		return fmt.Errorf("decision response action is required")
	}
	if !containsDecisionAction(request.Actions, r.Action) {
		return fmt.Errorf("decision action %q was not offered", r.Action)
	}
	if math.IsNaN(r.Uncertainty) || math.IsInf(r.Uncertainty, 0) || r.Uncertainty < 0 || r.Uncertainty > 1 {
		return fmt.Errorf("decision uncertainty must be in [0,1]")
	}
	if len(r.ModelOutput) > 0 && !json.Valid(r.ModelOutput) {
		return fmt.Errorf("decision model output is not valid JSON")
	}
	if r.Meta.Duration < 0 {
		return fmt.Errorf("decision duration must not be negative")
	}
	return nil
}

func containsDecisionAction(actions []string, wanted string) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}
