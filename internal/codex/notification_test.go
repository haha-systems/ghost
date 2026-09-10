package codex

import "testing"

func TestNotificationSummaryUsesStructuredItemContent(t *testing.T) {
	got := notificationSummary("item/started", map[string]any{"item": map[string]any{"type": "commandExecution", "command": "go test ./..."}})
	if got != "go test ./..." {
		t.Fatalf("summary=%q", got)
	}
	got = notificationSummary("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "content": []any{map[string]any{"type": "text", "text": "hello"}}}})
	if got != "hello" {
		t.Fatalf("message=%q", got)
	}
}
