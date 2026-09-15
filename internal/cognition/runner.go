package cognition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
)

type SessionConfigResolver func(context.Context, string, epistemic.Phase) (runtime.SessionConfig, error)

type PhaseRequest struct {
	WorkID     string
	Goal       string
	Phase      epistemic.Phase
	ResourceID string
	Projection epistemic.Projection
}

type PhaseResult struct {
	Artifact   PhaseArtifact
	Raw        []byte
	ResourceID string
	SessionID  string
	TurnID     string
	Runtime    string
	Model      string
}

type PhaseRunner struct {
	runtime runtime.Runtime
	resolve SessionConfigResolver
}

func NewPhaseRunner(rt runtime.Runtime, resolve SessionConfigResolver) *PhaseRunner {
	return &PhaseRunner{runtime: rt, resolve: resolve}
}

func (r *PhaseRunner) Run(ctx context.Context, request PhaseRequest) (PhaseResult, error) {
	if r == nil || r.runtime == nil || r.resolve == nil {
		return PhaseResult{}, errors.New("phase runner is not configured")
	}
	if request.Phase == "" || request.Projection.Phase != request.Phase {
		return PhaseResult{}, errors.New("phase request and projection do not match")
	}
	config, err := r.resolve(ctx, request.ResourceID, request.Phase)
	if err != nil {
		return PhaseResult{}, fmt.Errorf("resolve phase resource: %w", err)
	}
	if config.AgentID == "" {
		return PhaseResult{}, errors.New("phase resource has no runtime agent")
	}
	phaseInstructions, err := phaseInstruction(request.Phase)
	if err != nil {
		return PhaseResult{}, err
	}
	if config.Instructions != "" {
		config.Instructions += "\n\n"
	}
	config.Instructions += phaseInstructions
	session, err := r.runtime.Start(ctx, config)
	if err != nil {
		return PhaseResult{}, fmt.Errorf("start %s session: %w", request.Phase, err)
	}
	defer session.Close()
	prompt, err := json.Marshal(struct {
		WorkID     string          `json:"work_id"`
		Goal       string          `json:"goal"`
		Phase      epistemic.Phase `json:"phase"`
		Projection epistemic.State `json:"projection"`
	}{request.WorkID, request.Goal, request.Phase, request.Projection.State})
	if err != nil {
		return PhaseResult{}, err
	}
	if err := session.Send(ctx, runtime.Input{Text: string(prompt)}); err != nil {
		return PhaseResult{}, fmt.Errorf("send %s request: %w", request.Phase, err)
	}

	var output strings.Builder
	turnID := ""
	for {
		select {
		case <-ctx.Done():
			return PhaseResult{}, ctx.Err()
		case event, ok := <-session.Events():
			if !ok {
				return PhaseResult{}, errors.New("phase session ended before turn completion")
			}
			if event.SessionID != "" && event.SessionID != session.ID() {
				continue
			}
			if event.TurnID != "" {
				if turnID == "" {
					turnID = event.TurnID
				}
				if event.TurnID != turnID {
					continue
				}
			}
			method := event.Metadata["backend_method"]
			if event.Kind == runtime.KindMessage && event.Summary != "" {
				if method == "" || strings.Contains(method, "delta") {
					output.WriteString(event.Summary)
				}
				continue
			}
			if event.Kind == runtime.KindError {
				return PhaseResult{}, fmt.Errorf("phase runtime failed: %s", event.Summary)
			}
			if event.Kind == runtime.KindStatus {
				switch method {
				case "turn/failed":
					return PhaseResult{}, fmt.Errorf("%s turn failed: %s", request.Phase, event.Summary)
				case "turn/completed":
					raw := []byte(strings.TrimSpace(output.String()))
					artifact, err := ParsePhaseArtifact(request.Phase, raw)
					if err != nil {
						return PhaseResult{}, err
					}
					metadata := session.Metadata()
					return PhaseResult{Artifact: artifact, Raw: raw, ResourceID: request.ResourceID, SessionID: session.ID(), TurnID: turnID, Runtime: r.runtime.Name(), Model: metadata.Model}, nil
				}
			}
		}
	}
}

func phaseInstruction(phase epistemic.Phase) (string, error) {
	contract, ok := Contract(phase)
	if !ok {
		return "", fmt.Errorf("no phase contract for %q", phase)
	}
	return contract.String(), nil
}
