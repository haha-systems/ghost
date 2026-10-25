package run

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
)

type eventContext struct {
	Workspace      core.WorkspaceID
	AgentIDs       map[string]core.AgentID
	SessionIDs     map[string]core.SessionID
	ResourceAgents map[string]string
	TaskID         core.TaskID
	ThreadID       core.ThreadID
	CorrelationID  core.EventID
}

func adaptRuntimeEvent(ctx eventContext, source runtime.Event) (core.Event, error) {
	typeName := "runtime.event"
	actor := core.Actor{Kind: core.ActorSystem, ID: "runtime"}
	agentID := ctx.AgentIDs[source.AgentID]
	if agentID != "" {
		actor = core.Actor{Kind: core.ActorAgent, ID: string(agentID)}
	}
	switch source.Kind {
	case runtime.KindMessage:
		typeName = "conversation.agent_message"
	case runtime.KindThinking:
		typeName = "agent.thinking"
	case runtime.KindCommand, runtime.KindTool:
		typeName = "tool.call"
		method := strings.ToLower(source.Metadata["backend_method"])
		if strings.Contains(method, "completed") || strings.Contains(method, "failed") {
			typeName = "tool.result"
		}
	case runtime.KindFile:
		typeName = "tool.file_change"
	case runtime.KindUsage:
		typeName = "usage.tokens"
	case runtime.KindStatus, runtime.KindSession:
		typeName = "session.state"
	case runtime.KindError:
		typeName = "provider.error"
	}
	runtimePayload := source
	runtimePayload.Raw = nil
	payload := struct {
		Runtime     runtime.Event `json:"runtime"`
		Raw         any           `json:"raw,omitempty"`
		ToolSummary string        `json:"tool_summary,omitempty"`
	}{Runtime: runtimePayload, Raw: structuredRaw(source.Raw)}
	if typeName == "tool.call" || typeName == "tool.result" {
		payload.ToolSummary = structuredToolSummary(source.Raw)
	}
	return newCoreEvent(ctx, source.Time, actor, "runtime", typeName, agentID, ctx.SessionIDs[source.SessionID], payload)
}

func adaptLegacyEvent(ctx eventContext, agent string, source event.Event) (core.Event, error) {
	typeName := "runtime.event"
	agentID := ctx.AgentIDs[agent]
	actor := core.Actor{Kind: core.ActorSystem, ID: "ghost"}
	if agentID != "" {
		actor = core.Actor{Kind: core.ActorAgent, ID: string(agentID)}
	}
	switch source.Kind {
	case event.KindAgent, event.KindResponse:
		typeName = "conversation.agent_message"
	case event.KindSteering:
		typeName = "coordination.steering"
		actor, agentID = core.Actor{Kind: core.ActorOperator, ID: "you"}, ""
	case event.KindCommand, event.KindTool:
		typeName = "tool.call"
		if method := strings.ToLower(source.Metadata["backend_method"]); strings.Contains(method, "completed") || strings.Contains(method, "failed") {
			typeName = "tool.result"
		}
	case event.KindFile:
		typeName = "tool.file_change"
	case event.KindUsage:
		typeName = "usage.tokens"
	case event.KindThinking:
		typeName = "agent.thinking"
	case event.KindSession:
		typeName = "session.state"
	case event.KindStatus:
		typeName = "session.state"
		if source.Metadata["phase"] != "" {
			typeName = "ces.phase"
		}
	case event.KindQAC:
		typeName = "decision.qac"
	case event.KindCES, event.KindPhase:
		typeName = "ces.phase"
	case event.KindError:
		switch strings.ToUpper(source.Source) {
		case "QAC":
			typeName = "decision.qac_error"
		case "CES":
			typeName = "ces.phase"
		default:
			typeName = "provider.error"
		}
	}
	eventPayload := source
	eventPayload.Raw = nil
	payload := struct {
		Event       event.Event `json:"event"`
		Raw         any         `json:"raw,omitempty"`
		ToolSummary string      `json:"tool_summary,omitempty"`
	}{Event: eventPayload, Raw: structuredRaw(source.Raw)}
	if typeName == "tool.call" || typeName == "tool.result" {
		payload.ToolSummary = structuredToolSummary(source.Raw)
	}
	return newCoreEvent(ctx, source.Time, actor, source.Source, typeName, agentID, ctx.SessionIDs[source.SessionID], payload)
}

func adaptPhaseEvent(ctx eventContext, source cognition.PhaseEvent) (core.Event, error) {
	agentID := ctx.AgentIDs[source.AgentID]
	actor := core.Actor{Kind: core.ActorSystem, ID: "ces"}
	if agentID != "" {
		actor = core.Actor{Kind: core.ActorAgent, ID: string(agentID)}
	}
	return newCoreEvent(ctx, source.Time, actor, "CES", "ces.phase", agentID, ctx.SessionIDs[source.SessionID], source)
}

func adaptQACDecision(ctx eventContext, plan cognition.Plan) (core.Event, error) {
	agentID := ctx.AgentIDs[ctx.ResourceAgents[plan.To]]
	payload := struct {
		PlanID   string                   `json:"plan_id"`
		WorkID   string                   `json:"work_id,omitempty"`
		From     string                   `json:"from,omitempty"`
		To       string                   `json:"to"`
		Action   string                   `json:"action"`
		Initial  bool                     `json:"initial"`
		Decision any                      `json:"decision"`
		Request  cognition.QACRequest     `json:"request"`
		Jev      *cognition.JevAssessment `json:"jev,omitempty"`
	}{
		PlanID: plan.ID, WorkID: plan.WorkID, From: plan.From, To: plan.To,
		Action: string(plan.Action), Initial: plan.Initial, Decision: plan.Decision,
		Request: plan.Request, Jev: plan.Jev,
	}
	return newCoreEvent(ctx, timeNow(), core.Actor{Kind: core.ActorSystem, ID: "qac"}, "QAC", "decision.qac", agentID, "", payload)
}

func newCoreEvent(ctx eventContext, at time.Time, actor core.Actor, source, typeName string, agentID core.AgentID, sessionID core.SessionID, payload any) (core.Event, error) {
	if at.IsZero() {
		at = timeNow()
	}
	if source == "" {
		source = "runtime"
	}
	id, err := core.NewID("event")
	if err != nil {
		return core.Event{}, fmt.Errorf("make core event ID: %w", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return core.Event{}, fmt.Errorf("encode core event payload: %w", err)
	}
	return core.Event{
		ID: core.EventID(id), SchemaVersion: core.EventSchemaVersion, Time: at.UTC(),
		Workspace: ctx.Workspace, Actor: actor, Source: source, Type: typeName,
		Category: core.CategoryOf(typeName), TaskID: ctx.TaskID, SessionID: sessionID,
		AgentID: agentID, ThreadID: ctx.ThreadID, CorrelationID: ctx.CorrelationID,
		Payload: data,
	}, nil
}

func structuredToolSummary(raw json.RawMessage) string {
	if !json.Valid(raw) {
		return ""
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	item, _ := value["item"].(map[string]any)
	if item == nil {
		item = value
	}
	for _, key := range []string{"command", "path", "filePath", "file_path"} {
		if text, ok := item[key].(string); ok && text != "" {
			return text
		}
	}
	name, _ := item["name"].(string)
	if name == "" {
		name, _ = item["toolName"].(string)
	}
	if name == "" {
		return ""
	}
	input := item["arguments"]
	if input == nil {
		input = item["input"]
	}
	if input == nil {
		return name
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return name
	}
	return name + " " + string(encoded)
}

func structuredRaw(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	if json.Valid(raw) {
		return json.RawMessage(append([]byte(nil), raw...))
	}
	return struct {
		Base64 []byte `json:"base64"`
	}{Base64: append([]byte(nil), raw...)}
}
