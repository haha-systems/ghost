package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/core"
)

// JevAgent normalizes Jev's bounded judgment for the decision service.
type JevAgent struct{ client cognition.JevClient }

func NewJevAgent(client cognition.JevClient) *JevAgent { return &JevAgent{client: client} }

func (a *JevAgent) Info() Info {
	return Info{ID: "jev", Version: "1", Kinds: []core.DecisionKind{core.DecisionKindCognitiveAdequacy}, Capabilities: []core.Capability{core.CapabilityDecisionSupport}}
}

func (a *JevAgent) Decide(ctx context.Context, request core.DecisionRequest) (core.DecisionResponse, error) {
	if request.Kind != core.DecisionKindCognitiveAdequacy {
		return core.DecisionResponse{}, invalidOutput("Jev cannot answer kind %q", request.Kind)
	}
	if a == nil || a.client == nil {
		return core.DecisionResponse{}, errors.New("no Jev client configured")
	}
	decoder := json.NewDecoder(bytes.NewReader(request.State))
	decoder.DisallowUnknownFields()
	var input cognition.JevInput
	if err := decoder.Decode(&input); err != nil {
		return core.DecisionResponse{}, invalidOutput("decode Jev input: %v", err)
	}
	var raw []byte
	raw, err := a.client.Assess(ctx, input)
	if err != nil {
		return core.DecisionResponse{}, err
	}
	judgment, err := cognition.ParseJevJudgment(raw)
	if err != nil {
		return core.DecisionResponse{}, invalidOutput("parse Jev judgment: %v", err)
	}
	return core.DecisionResponse{
		RequestID: request.ID, Action: string(judgment.Judgment), Uncertainty: 1 - judgment.Confidence,
		Supporting: map[string]string{
			"current_resource_adequacy":             strconv.FormatFloat(judgment.CurrentResourceAdequacy, 'f', -1, 64),
			"expected_gain_from_stronger_cognition": strconv.FormatFloat(judgment.ExpectedGainFromStrongerCognition, 'f', -1, 64),
			"decomposition_need":                    strconv.FormatFloat(judgment.DecompositionNeed, 'f', -1, 64),
			"confidence":                            strconv.FormatFloat(judgment.Confidence, 'f', -1, 64),
		},
		ModelOutput: append(json.RawMessage(nil), raw...),
		Meta:        core.DecisionMeta{AgentID: "jev", Version: "1"},
	}, nil
}
