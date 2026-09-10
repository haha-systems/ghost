// Package theme contains the visual vocabulary shared by Ghost's views.
package theme

import "charm.land/lipgloss/v2"

// Theme is the complete visual definition used by the application.
//
// Keeping this boundary small makes it possible to add external themes later
// without coupling views to a particular palette or set of glyphs.
type Theme struct {
	Name    string
	Colors  Colors
	Symbols Symbols
	Borders Borders
}

// Colors is the Bloodwire palette. Values are hex strings so callers can pass
// them directly to lipgloss.Color without duplicating colour literals.
type Colors struct {
	Background string
	Surface    string

	Text      string
	TextMuted string

	AccentDim string
	Accent    string
	AccentHot string

	Border  string
	Error   string
	Warning string
	Success string
}

// Symbols are the glyphs used to convey state without adding a colour system.
type Symbols struct {
	Active    string
	Idle      string
	Important string
	Complete  string
	Error     string

	Cursor  string
	Prompt  string
	Divider string
}

// Borders keeps border geometry in the theme, while views remain responsible
// for deciding when a border is useful. Minimal is intentionally understated.
type Borders struct {
	Panel     lipgloss.Border
	Focused   lipgloss.Border
	Input     lipgloss.Border
	Separator lipgloss.Border

	// Rounded and Thin are named variants for components that need a more
	// explicit choice than the default panel border.
	Rounded lipgloss.Border
	Thin    lipgloss.Border
}

// Frame applies the theme background and fills the available terminal area.
// Width and height include the frame's padding, so child content never leaks
// the terminal's default background around a Ghost screen.
func Frame(th Theme, width, height int, content string) string {
	frame := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Colors.Text)).
		Background(lipgloss.Color(th.Colors.Background)).
		Padding(1, 2)
	if width > 0 {
		frame = frame.Width(width)
	}
	if height > 0 {
		frame = frame.Height(height)
	}
	return frame.Render(content)
}
