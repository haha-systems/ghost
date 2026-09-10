package theme_test

import (
	"testing"

	"github.com/haha-systems/ghost/internal/ui/theme"
)

func TestBloodwireIsComplete(t *testing.T) {
	t.Parallel()

	got := theme.Bloodwire()
	if got.Name != "Bloodwire" {
		t.Fatalf("theme name = %q, want Bloodwire", got.Name)
	}

	for name, value := range map[string]string{
		"background": got.Colors.Background,
		"surface":    got.Colors.Surface,
		"text":       got.Colors.Text,
		"text muted": got.Colors.TextMuted,
		"accent dim": got.Colors.AccentDim,
		"accent":     got.Colors.Accent,
		"accent hot": got.Colors.AccentHot,
		"border":     got.Colors.Border,
		"active":     got.Symbols.Active,
		"idle":       got.Symbols.Idle,
		"important":  got.Symbols.Important,
		"complete":   got.Symbols.Complete,
		"error":      got.Symbols.Error,
	} {
		if value == "" {
			t.Errorf("%s is empty", name)
		}
	}

	if got.Borders.Panel.Top == "" || got.Borders.Separator.Bottom == "" {
		t.Fatal("Bloodwire borders are incomplete")
	}
}
