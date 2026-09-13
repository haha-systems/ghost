// Package chrome renders the shared furniture of Ghost's screens: the meta
// sidebar, pane titles, rules, and width-aware text fitting.
package chrome

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/haha-systems/ghost/internal/ui/layout"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

// Row is one line of the sidebar. A Label with no Value renders as a heading.
type Row struct {
	Label, Value string
	// Style overrides the value colour; empty uses the default text colour.
	Style string
	// Heading renders the row as a muted section title.
	Heading bool
	// Rule renders a horizontal divider and ignores every other field.
	Rule bool
}

// Truncate fits s into width cells, accounting for wide runes and any escape
// sequences already embedded in s.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// Pad fits s to exactly width cells, truncating or padding as needed.
func Pad(s string, width int) string {
	s = Truncate(s, width)
	if gap := width - ansi.StringWidth(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// Rule renders a full-width horizontal divider.
func Rule(th theme.Theme, width int) string {
	if width <= 0 {
		return ""
	}
	return style(th, th.Colors.Border).Render(strings.Repeat("─", width))
}

// PaneTitle renders a pane heading, marked with the cursor glyph when focused.
// A badge is right-aligned on the same row; it carries the new-entry indicator,
// which only a pane that has stopped following has anything to show.
func PaneTitle(th theme.Theme, label, badge string, focused bool, width int) string {
	prefix := " "
	if focused {
		prefix = th.Symbols.Cursor
	}
	colour := th.Colors.TextMuted
	if focused {
		colour = th.Colors.Accent
	}
	title := style(th, colour).Bold(true).Render(prefix + " " + label)
	if badge == "" {
		return Pad(title, width)
	}
	rendered := style(th, th.Colors.Warning).Render(badge)
	gap := width - ansi.StringWidth(title) - ansi.StringWidth(rendered)
	if gap < 1 {
		return Pad(title, width)
	}
	return title + strings.Repeat(" ", gap) + rendered
}

// FocusRule renders the divider below a pane, accented while it holds focus so
// the focused region is identifiable without boxing every pane in.
func FocusRule(th theme.Theme, width int, focused bool) string {
	if width <= 0 {
		return ""
	}
	colour := th.Colors.Border
	if focused {
		colour = th.Colors.Accent
	}
	return style(th, colour).Render(strings.Repeat("─", width))
}

// Sidebar renders rows into a fixed-width column of exactly height lines.
func Sidebar(th theme.Theme, rows []Row, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := make([]string, 0, height)
	for _, row := range rows {
		if len(lines) == height {
			break
		}
		lines = append(lines, sidebarLine(th, row, width))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func sidebarLine(th theme.Theme, row Row, width int) string {
	switch {
	case row.Rule:
		return Rule(th, width)
	case row.Heading:
		return style(th, th.Colors.TextMuted).Bold(true).Render(Pad(row.Label, width))
	case row.Value == "":
		return style(th, th.Colors.Text).Render(Pad(row.Label, width))
	}
	// Labels occupy a fixed gutter so values align down the column.
	const labelWidth = 9
	label := style(th, th.Colors.TextMuted).Render(Pad(row.Label, labelWidth))
	colour := row.Style
	if colour == "" {
		colour = th.Colors.Text
	}
	value := style(th, colour).Render(Truncate(row.Value, maxInt(width-labelWidth-1, 1)))
	line := label + " " + value
	if gap := width - ansi.StringWidth(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return line
}

// Columns joins a sidebar and a content column with the standard gutter. When
// the sidebar is empty the content column is returned unchanged.
func Columns(th theme.Theme, sidebar, content string, sidebarWidth int) string {
	if sidebarWidth <= 0 || sidebar == "" {
		return content
	}
	// The divider must run the full height of the columns, so it is built per
	// line rather than joined as a single-line block.
	height := len(strings.Split(sidebar, "\n"))
	if n := len(strings.Split(content, "\n")); n > height {
		height = n
	}
	rule := style(th, th.Colors.Border).Render(" │ ")
	gutter := make([]string, height)
	for i := range gutter {
		gutter[i] = rule
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, strings.Join(gutter, "\n"), content)
}

// Fit forces block to exactly height lines and width cells per line, so a pane
// can never push the rest of the screen out of the frame.
func Fit(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = Pad(line, width)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// TooSmall renders the guidance shown when the terminal cannot host a screen.
func TooSmall(th theme.Theme, width, height int) string {
	msg := "Terminal too small.\nMinimum size: " +
		itoa(layout.MinWidth) + "x" + itoa(layout.MinHeight) + "."
	return theme.Frame(th, width, height, style(th, th.Colors.TextMuted).Render(msg))
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func style(th theme.Theme, colour string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colour))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
