package chrome

import (
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/ui/theme"
)

func TestSidebarWrapsLongValuesAtNarrowWidths(t *testing.T) {
	got := Sidebar(theme.Bloodwire(), []Row{{Label: "Goal", Value: "preserve the full dashboard goal"}}, 20, 5)
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5", len(lines))
	}
	if !strings.Contains(got, "preserve") {
		t.Fatalf("wrapped value missing: %q", got)
	}
	for i, line := range lines {
		if len([]rune(line)) < 20 {
			t.Fatalf("line %d width = %d, want at least 20: %q", i, len([]rune(line)), line)
		}
	}
}
