package agentdetail

import (
	"os"
	"testing"

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

	m = m.AddTypedLog("thought", "The steering budget and the pane budget must come from one place.")
	m = m.AppendLog("I refactored the row budget into internal/ui/layout so both screens share it.")
	m = m.AddTypedLog("command", "go test ./internal/ui/... -race")
	m = m.AddTypedLog("file", "write internal/ui/layout/layout.go")
	m = m.AddTypedLog("tool", "search_symbols steeringHeight")
	m = m.AddTypedLog("command", "gofmt -l internal/")
	m = m.AddTypedLog("qac", "ESCALATE VEIL → ANALYST  0.82 / 0.70")
	m = m.AddTypedLog("error", "turn failed: context deadline exceeded")
	t.Log("\n" + m.View())
}
