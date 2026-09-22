package codex

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

func normalizeNotification(n notification, agentID, sessionID, turnIDValue string) runtime.Event {
	var p map[string]any
	_ = json.Unmarshal(n.Params, &p)
	return normalizeNotificationPayload(n, agentID, sessionID, turnIDValue, p)
}

func normalizeNotificationPayload(n notification, agentID, sessionID, turnIDValue string, p map[string]any) runtime.Event {
	// Only payload data can establish causal scope. A local guard placeholder
	// must not scope an otherwise unscoped notification.
	if id := turnID(p); id != "" {
		turnIDValue = id
	} else {
		turnIDValue = ""
	}
	metadata := map[string]string{"backend_method": n.Method}
	for key, value := range rateLimitMetadata(p) {
		metadata[key] = value
	}
	return runtime.Event{Time: time.Now(), AgentID: agentID, SessionID: sessionID, TurnID: turnIDValue, Kind: normalizeKindFromPayload(n.Method, p), Summary: notificationSummary(n.Method, p), Metadata: metadata, Raw: n.Params}
}

func normalizeKindFromPayload(method string, p map[string]any) runtime.EventKind {
	if item, ok := p["item"].(map[string]any); ok {
		switch item["type"] {
		case "agentMessage":
			return runtime.KindMessage
		case "reasoning":
			return runtime.KindThinking
		case "commandExecution":
			return runtime.KindCommand
		case "fileChange", "fileRead":
			return runtime.KindFile
		case "toolCall", "mcpToolCall":
			return runtime.KindTool
		}
	}
	return normalizeKind(method)
}

func normalizeKind(method string) runtime.EventKind {
	m := strings.ToLower(method)
	switch {
	case strings.Contains(m, "agentmessage"):
		return runtime.KindMessage
	case strings.Contains(m, "reasoning"):
		return runtime.KindThinking
	case strings.Contains(m, "command"), strings.Contains(m, "exec"):
		return runtime.KindCommand
	case strings.Contains(m, "file"), strings.Contains(m, "read"):
		return runtime.KindFile
	case strings.Contains(m, "tool"):
		return runtime.KindTool
	case strings.Contains(m, "usage"):
		return runtime.KindUsage
	case strings.Contains(m, "error"), strings.Contains(m, "failed"):
		return runtime.KindError
	default:
		return runtime.KindStatus
	}
}
