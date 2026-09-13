package agentdetail

import (
	"os"
	"testing"

	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/history"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

// TestRenderPreview prints a rendered detail screen for manual inspection:
//
//	GHOST_PREVIEW=1 go test ./internal/ui/agentdetail/ -run RenderPreview -v
func TestRenderPreview(t *testing.T) {
	if os.Getenv("GHOST_PREVIEW") == "" {
		t.Skip("set GHOST_PREVIEW=1 to print a rendered screen")
	}
	m := New(theme.Bloodwire(), keymap.Default()).ClearLogs().SetAgent(ghostmodel.Agent{
		ID: "veil", Callsign: "VEIL", Client: "CODEX", Model: "gpt-5-codex",
		SessionID: "thread-01J9X", Runtime: "18m 42s", State: ghostmodel.AgentActive,
		Activity: "running the layout suite",
	}).SetWork("active").SetSize(120, 34)

	m = m.SetHistory([]history.Entry{
		{AgentID: "veil", Kind: event.KindThinking, Message: "The steering budget and the pane budget must come from one place."},
		{AgentID: "veil", Kind: event.KindResponse, Message: "I refactored the row budget into internal/ui/layout so both screens share it."},
		{AgentID: "veil", Kind: event.KindCommand, Message: "go test ./internal/ui/... -race"},
		{AgentID: "veil", Kind: event.KindFile, Message: "write internal/ui/layout/layout.go"},
		{AgentID: "veil", Kind: event.KindTool, Message: "search_symbols steeringHeight"},
		{AgentID: "veil", Kind: event.KindCommand, Message: "gofmt -l internal/"},
		{AgentID: "veil", Kind: event.KindQAC, Message: "ESCALATE VEIL → ANALYST  0.82 / 0.70"},
		{AgentID: "veil", Kind: event.KindError, Message: "turn failed: context deadline exceeded"},
	})
	t.Log("\n" + m.View())
}
