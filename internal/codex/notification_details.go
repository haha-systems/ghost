package codex

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

func mergeRateLimitMaps(previous, incoming map[string]any) map[string]any {
	result := cloneMap(previous)
	for key, value := range incoming {
		if nested, ok := value.(map[string]any); ok {
			prior, _ := result[key].(map[string]any)
			result[key] = mergeRateLimitMaps(prior, nested)
			continue
		}
		result[key] = value
	}
	return result
}

func cloneMap(values map[string]any) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		if nested, ok := value.(map[string]any); ok {
			result[key] = cloneMap(nested)
			continue
		}
		result[key] = value
	}
	return result
}

func turnID(p map[string]any) string {
	if id, ok := p["turnId"].(string); ok {
		return id
	}
	if turn, ok := p["turn"].(map[string]any); ok {
		if id, ok := turn["id"].(string); ok {
			return id
		}
	}
	return ""
}

func notificationSummary(method string, p map[string]any) string {
	if method == "account/rateLimits/updated" {
		if summary := rateLimitSummary(p); summary != "" {
			return summary
		}
	}
	if d, ok := p["delta"].(string); ok {
		return d
	}
	if method == "turn/completed" {
		return "turn completed"
	}
	if method == "turn/failed" {
		return "turn failed"
	}
	if item, ok := p["item"].(map[string]any); ok {
		typ, _ := item["type"].(string)
		if text, ok := item["text"].(string); ok && text != "" {
			return text
		}
		switch typ {
		case "commandExecution":
			if v, ok := item["command"].(string); ok {
				if status, ok := item["status"].(string); ok && status != "" {
					return v + " (" + status + ")"
				}
				return v
			}
		case "agentMessage":
			if content, ok := item["content"].([]any); ok {
				for _, v := range content {
					if x, ok := v.(map[string]any); ok {
						if t, ok := x["text"].(string); ok {
							return t
						}
					}
				}
			}
		case "reasoning":
			return "reasoning"
		default:
			if typ != "" {
				return typ
			}
		}
	}
	for _, key := range []string{"message", "error", "status"} {
		if v, ok := p[key].(string); ok && v != "" {
			return v
		}
	}
	return method
}

func rateLimitSummary(p map[string]any) string {
	limits, ok := rateLimitMap(p)
	if !ok {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, name := range []string{"primary", "secondary"} {
		window, ok := mapValue(limits, name).(map[string]any)
		if !ok {
			continue
		}
		used, ok := numberValue(window, "usedPercent", "used_percent")
		if !ok {
			continue
		}
		part := fmt.Sprintf("%s: %s%% remaining (%s%% used)", name, formatNumber(100-used), formatNumber(used))
		if duration, ok := numberValue(window, "windowDurationMins", "window_minutes"); ok {
			part += fmt.Sprintf(", %sm window", formatNumber(duration))
		}
		if reset, ok := numberValue(window, "resetsAt", "resets_at"); ok {
			part += ", resets " + time.Unix(int64(reset), 0).UTC().Format(time.RFC3339)
		}
		parts = append(parts, part)
	}
	if credits, ok := mapValue(limits, "credits").(map[string]any); ok {
		if balance, exists := stringValue(credits, "balance"); exists {
			parts = append(parts, "credits: balance "+balance)
		} else if unlimited, exists := boolValue(credits, "unlimited"); exists && unlimited {
			parts = append(parts, "credits: unlimited")
		}
	}
	return strings.Join(parts, "; ")
}

func rateLimitMetadata(p map[string]any) map[string]string {
	result := map[string]string{}
	limits, ok := rateLimitMap(p)
	if !ok {
		return result
	}
	for _, name := range []string{"primary", "secondary"} {
		window, ok := mapValue(limits, name).(map[string]any)
		if !ok {
			continue
		}
		if used, ok := numberValue(window, "usedPercent", "used_percent"); ok {
			result["rate_"+name+"_used_percent"] = formatNumber(used)
			result["rate_"+name+"_remaining_percent"] = formatNumber(100 - used)
		}
		if duration, ok := numberValue(window, "windowDurationMins", "window_minutes"); ok {
			result["rate_"+name+"_window_minutes"] = formatNumber(duration)
		}
		if reset, ok := numberValue(window, "resetsAt", "resets_at"); ok {
			result["rate_"+name+"_resets_at"] = strconv.FormatInt(int64(reset), 10)
		}
	}
	return result
}

func rateLimitMap(p map[string]any) (map[string]any, bool) {
	if limits, ok := mapValue(p, "rateLimits").(map[string]any); ok {
		return limits, true
	}
	if limits, ok := mapValue(p, "rate_limits").(map[string]any); ok {
		return limits, true
	}
	return nil, false
}

func mapValue(values map[string]any, key string) any { return values[key] }

func numberValue(values map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		switch value := values[key].(type) {
		case float64:
			return value, true
		case int:
			return float64(value), true
		case int64:
			return float64(value), true
		}
	}
	return 0, false
}

func stringValue(values map[string]any, key string) (string, bool) {
	value, ok := values[key].(string)
	return value, ok
}

func boolValue(values map[string]any, key string) (bool, bool) {
	value, ok := values[key].(bool)
	return value, ok
}

func formatNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

func samePresentation(a, b runtime.Event) bool {
	return a.Kind == runtime.KindMessage && b.Kind == runtime.KindMessage && a.AgentID == b.AgentID && a.SessionID != "" && a.SessionID == b.SessionID && a.TurnID != "" && a.TurnID == b.TurnID
}
