package dashboard

import (
	"testing"

	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

func TestSetAgentsClampsSelection(t *testing.T) {
	agents := []ghostmodel.Agent{{ID: "one"}, {ID: "two"}, {ID: "three"}}
	m := New(theme.Bloodwire(), keymap.Default(), agents, nil)
	m.selected = 2
	m = m.SetAgents(agents[:1])
	if got := m.SelectedIndex(); got != 0 {
		t.Fatalf("selected = %d, want 0", got)
	}
}
