package dashboard

import (
	"os"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/event"
)

// TestRenderPreview prints a rendered dashboard for manual inspection:
//
//	go test ./internal/ui/dashboard/ -run RenderPreview -v
func TestRenderPreview(t *testing.T) {
	if os.Getenv("GHOST_PREVIEW") == "" {
		t.Skip("set GHOST_PREVIEW=1 to print a rendered screen")
	}
	m := newModel().SetSize(120, 34).SetQAC(true, "analyst").SetWork("active", "refactor the console layout")
	now := time.Now()
	events := []event.Event{
		{Time: now, Source: "VEIL", Kind: event.KindSession, Message: "session ready"},
		{Time: now, Source: "VEIL", Kind: event.KindThinking, Message: "considering the layout budget"},
		{Time: now, Source: "VEIL", Kind: event.KindCommand, Message: "go test ./... -run Layout"},
		{Time: now, Source: "VEIL", Kind: event.KindFile, Message: "read internal/ui/layout/layout.go"},
		{Time: now, Source: "WRAITH", Kind: event.KindTool, Message: "search_symbols steeringHeight"},
		{Time: now, Source: "WRAITH", Kind: event.KindResponse, Message: "The steering editor was claiming twenty rows regardless of content."},
		{Time: now, Source: "QAC", Kind: event.KindQAC, Message: "ESCALATE VEIL → ANALYST  0.82 / 0.70"},
		{Time: now, Source: "SHADE", Kind: event.KindError, Message: "turn failed: context deadline exceeded"},
	}
	t.Log("\n" + m.SetEvents(events).View())
}
