//go:build live

package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/core"
)

// openAICompatibleJev is a test-only JevClient for a local OpenAI-compatible
// chat endpoint (LM Studio, llama-server, Ollama). It is not a production Jev
// transport; see docs/design/jev-transport.md for that proposal.
type openAICompatibleJev struct{ base, model string }

func (c openAICompatibleJev) Assess(ctx context.Context, input cognition.JevInput) ([]byte, error) {
	state, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"model": c.model, "temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": cognition.JevContract + " Reply with one JSON object only, with exactly these keys: current_resource_adequacy, expected_gain_from_stronger_cognition, decomposition_need, confidence (numbers in [0,1]) and judgment (one of continue_current_resource, escalate_cognition, reframe_or_decompose, insufficient_evidence)."},
			{"role": "user", "content": string(state)},
		},
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.base, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chat completions: HTTP %d", response.StatusCode)
	}
	var decoded struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("chat completions: unexpected response")
	}
	content := strings.TrimSpace(decoded.Choices[0].Message.Content)
	content = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```")
	return []byte(strings.TrimSpace(content)), nil
}

// TestLiveDecisionModelJevJudgment sends one compact Jev request to a local
// OpenAI-compatible model through the real decision service and the strict
// Jev parser. It refuses non-loopback URLs so it cannot reach a paid API.
//
//	GHOST_LIVE=1                       required
//	GHOST_LIVE_DECISION_URL            e.g. http://127.0.0.1:1234/v1 (LM Studio)
//	GHOST_LIVE_DECISION_MODEL          model name served by that endpoint
func TestLiveDecisionModelJevJudgment(t *testing.T) {
	if os.Getenv("GHOST_LIVE") != "1" {
		t.Skip("live smoke test: set GHOST_LIVE=1 (and -tags live)")
	}
	base, model := os.Getenv("GHOST_LIVE_DECISION_URL"), os.Getenv("GHOST_LIVE_DECISION_MODEL")
	if base == "" || model == "" {
		t.Skip("set GHOST_LIVE_DECISION_URL and GHOST_LIVE_DECISION_MODEL to a local OpenAI-compatible endpoint")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		t.Fatalf("refusing non-loopback decision endpoint %q", base)
	}
	service := NewService(ServiceConfig{Enabled: map[core.DecisionKind]bool{core.DecisionKindCognitiveAdequacy: true}, Timeout: 2 * time.Minute})
	if err := service.Register(NewJevAgent(openAICompatibleJev{base: base, model: model})); err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(cognition.JevInput{Phase: "execute", CurrentResource: "veil", RepeatedAttempts: 3, ArtifactValid: true, ArtifactAccepted: true, ProgressState: cognition.ProgressLow, ProgressProvenance: cognition.ProgressStructuredExecution, ProgressStrength: .8, OpenUnknownCount: 2})
	outcome := service.Decide(context.Background(), core.DecisionRequest{
		ID: "event_00000000000000010000000000000001", Kind: core.DecisionKindCognitiveAdequacy,
		TaskID: "task_00000000000000010000000000000001", RequestingAgent: "agent_00000000000000010000000000000001",
		State: state, Actions: []string{"continue_current_resource", "escalate_cognition", "reframe_or_decompose", "insufficient_evidence"},
	})
	t.Logf("outcome status=%s reason=%q", outcome.Status, outcome.Reason)
	if outcome.Response == nil {
		t.Fatalf("no valid Jev judgment: %+v", outcome)
	}
	t.Logf("action=%s uncertainty=%.2f model_output=%s", outcome.Response.Action, outcome.Response.Uncertainty, outcome.Response.ModelOutput)
}
