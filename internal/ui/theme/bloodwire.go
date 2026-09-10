package theme

import "charm.land/lipgloss/v2"

// Bloodwire returns Ghost's restrained default theme.
func Bloodwire() Theme {
	thin := lipgloss.Border{
		Top: "─", Bottom: "─", Left: "│", Right: "│",
		TopLeft: "┌", TopRight: "┐", BottomLeft: "└", BottomRight: "┘",
	}
	rounded := lipgloss.Border{
		Top: "─", Bottom: "─", Left: "│", Right: "│",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯",
	}
	separator := lipgloss.Border{Bottom: "─"}
	return Theme{
		Name: "Bloodwire",
		Colors: Colors{
			Background: "#070506",
			Surface:    "#0D080A",
			Text:       "#E8DFE1",
			TextMuted:  "#73545B",
			AccentDim:  "#681323",
			Accent:     "#C51F3B",
			AccentHot:  "#FF3455",
			Border:     "#351017",
			Error:      "#FF3455",
			Warning:    "#C77A35",
			Success:    "#A9B86C",
		},
		Symbols: Symbols{
			Active:    "●",
			Idle:      "◌",
			Important: "◆",
			Complete:  "✓",
			Error:     "×",
			Cursor:    "▸",
			Prompt:    "›",
			Divider:   "·",
		},
		Borders: Borders{
			Panel:     rounded,
			Focused:   rounded,
			Input:     rounded,
			Separator: separator,
			Rounded:   rounded,
			Thin:      thin,
		},
	}
}
