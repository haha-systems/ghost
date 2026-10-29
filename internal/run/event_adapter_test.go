package run

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/qac"
)

func TestAdaptRuntimeEvent(t *testing.T) {
	ctx := testEventContext(t)
	tests := []struct {
		name     string
		in       runtime.Event
		typeName string
		category core.Category
	}{
		{name: "agent message", in: runtime.Event{AgentID: "shade", SessionID: "provider-session", TurnID: "turn-1", Kind: runtime.KindMessage}, typeName: "conversation.agent_message", category: core.CategoryConversation},
		{name: "tool call", in: runtime.Event{AgentID: "shade", SessionID: "provider-session", Kind: runtime.KindCommand, Raw: []byte(`{"item":{"type":"commandExecution","command":"go test ./...","status":"inProgress"}}`)}, typeName: "tool.call", category: core.CategoryTool},
		{name: "session state", in: runtime.Event{AgentID: "shade", SessionID: "provider-session", Kind: runtime.KindStatus}, typeName: "session.state", category: core.CategoryCoordination},
		{name: "provider error", in: runtime.Event{AgentID: "shade", SessionID: "provider-session", Kind: runtime.KindError}, typeName: "provider.error", category: core.CategoryCoordination},
		{name: "token usage", in: runtime.Event{AgentID: "shade", SessionID: "provider-session", Kind: runtime.KindUsage}, typeName: "usage.tokens", category: core.CategoryCoordination},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := adaptRuntimeEvent(ctx, tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Type != tt.typeName || got.Category != tt.category {
				t.Fatalf("event type/category = %q/%q", got.Type, got.Category)
			}
			if got.AgentID != ctx.AgentIDs["shade"] || got.SessionID != ctx.SessionIDs["provider-session"] || got.TaskID != ctx.TaskID || got.ThreadID != ctx.ThreadID || got.CorrelationID != ctx.CorrelationID {
				t.Fatalf("scope IDs were not mapped: %#v", got)
			}
			if !json.Valid(got.Payload) {
				t.Fatalf("payload is not JSON: %s", got.Payload)
			}
			if tt.name == "tool call" {
				var payload map[string]any
				if err := json.Unmarshal(got.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload["tool_summary"] != "go test ./..." {
					t.Fatalf("tool summary = %v", payload["tool_summary"])
				}
				if raw, ok := payload["raw"].(map[string]any); !ok || raw["item"].(map[string]any)["command"] != "go test ./..." {
					t.Fatalf("structured provider data was not preserved as JSON: %#v", payload["raw"])
				}
			}
		})
	}
}

func TestAdaptLegacyAndCESDecisionEvents(t *testing.T) {
	ctx := testEventContext(t)
	legacy, err := adaptLegacyEvent(ctx, "shade", event.Event{Time: time.Now(), Source: "QAC", Kind: event.KindQAC, Message: "escalate"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Type != "decision.qac" || legacy.Category != core.CategoryDecision || legacy.AgentID != ctx.AgentIDs["shade"] {
		t.Fatalf("legacy QAC mapping = %#v", legacy)
	}

	phase, err := adaptPhaseEvent(ctx, cognition.PhaseEvent{Time: time.Now(), Name: cognition.PhaseTurnRequested, RunID: "phase-run", AgentID: "shade", SessionID: "phase-session", Phase: "TRIAGE"})
	if err != nil {
		t.Fatal(err)
	}
	if phase.Type != "ces.phase" || phase.Category != core.CategoryCoordination || phase.AgentID != ctx.AgentIDs["shade"] || phase.TaskID != ctx.TaskID || phase.ThreadID != ctx.ThreadID || phase.CorrelationID != ctx.CorrelationID {
		t.Fatalf("phase mapping = %#v", phase)
	}
}

func TestAdaptQACDecisionPreservesStructuredChoice(t *testing.T) {
	ctx := testEventContext(t)
	ctx.ResourceAgents = map[string]string{"shade-resource": "shade"}
	got, err := adaptQACDecision(ctx, cognition.Plan{
		ID: "plan-2", WorkID: "work-1", From: "wraith-resource", To: "shade-resource",
		Action: qac.ActionEscalate, Decision: qac.Decision{From: "wraith-resource", To: "shade-resource", Action: qac.ActionEscalate, Score: .8, Threshold: .6, Reason: "higher capability"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "decision.qac" || got.Category != core.CategoryDecision || got.AgentID != ctx.AgentIDs["shade"] || got.CorrelationID != ctx.CorrelationID {
		t.Fatalf("QAC mapping = %#v", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["action"] != "escalate" || payload["to"] != "shade-resource" || payload["decision"] == nil {
		t.Fatalf("structured decision missing: %s", got.Payload)
	}
}

func testEventContext(t *testing.T) eventContext {
	t.Helper()
	workspace := core.WorkspaceID(testID(t, "workspace"))
	agent := core.AgentID(testID(t, "agent"))
	session := core.SessionID(testID(t, "session"))
	task := core.TaskID(testID(t, "task"))
	thread := core.ThreadID(testID(t, "thread"))
	correlation := core.EventID(testID(t, "event"))
	return eventContext{Workspace: workspace, AgentIDs: map[string]core.AgentID{"shade": agent}, SessionIDs: map[string]core.SessionID{"provider-session": session, "phase-session": session}, TaskID: task, ThreadID: thread, CorrelationID: correlation}
}

func testID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := core.NewID(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
