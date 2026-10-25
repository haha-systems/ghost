package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haha-systems/ghost/internal/core"
)

// RulesAgent uses a fixed heuristic: CES is on for goals with at least 20
// words, a numbered list, or the phrase "and then". Other goals select off.
type RulesAgent struct{}

func NewRulesAgent() *RulesAgent { return &RulesAgent{} }

func (*RulesAgent) Info() Info {
	return Info{ID: "rules", Version: "1", Kinds: []core.DecisionKind{core.DecisionKindCESMode}, Capabilities: []core.Capability{core.CapabilityDecisionSupport}}
}

func (*RulesAgent) Decide(_ context.Context, request core.DecisionRequest) (core.DecisionResponse, error) {
	if request.Kind != core.DecisionKindCESMode {
		return core.DecisionResponse{}, invalidOutput("rules agent cannot answer kind %q", request.Kind)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(request.State, &state); err != nil {
		return core.DecisionResponse{}, invalidOutput("decode CES decision state: %v", err)
	}
	var goal string
	if raw := state["goal"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &goal); err != nil {
			return core.DecisionResponse{}, invalidOutput("decode CES goal: %v", err)
		}
	}
	words := strings.Fields(goal)
	lower := strings.ToLower(goal)
	list := hasNumberedLine(goal)
	sequence := strings.Contains(lower, "and then")
	long := len(words) >= 20
	action := "ces_off"
	if list || sequence || long {
		action = "ces_on"
	}
	uncertainty := .2
	if !list && !sequence {
		margin := len(words) - 20
		if margin < 0 {
			margin = -margin
		}
		uncertainty = .5 - float64(margin)*.015
		if uncertainty < .1 {
			uncertainty = .1
		}
		if uncertainty > .5 {
			uncertainty = .5
		}
	}
	return core.DecisionResponse{
		RequestID: request.ID, Action: action, Uncertainty: uncertainty,
		Supporting: map[string]string{
			"heuristic":     "20-word threshold, numbered list, or 'and then'",
			"word_count":    fmt.Sprint(len(words)),
			"numbered_list": fmt.Sprint(list),
			"and_then":      fmt.Sprint(sequence),
		},
		Meta: core.DecisionMeta{AgentID: "rules", Version: "1"},
	}, nil
}

func hasNumberedLine(value string) bool {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		i := 0
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			i++
		}
		if i > 0 && i+1 < len(line) && (line[i] == '.' || line[i] == ')') && line[i+1] == ' ' {
			return true
		}
	}
	return false
}
