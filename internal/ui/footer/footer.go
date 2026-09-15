// Package footer builds Ghost's contextual shortcut row.
//
// The footer answers one question: what actions are valid here, right now. It
// is generated from the current screen, focus, and runtime state rather than
// embedded as a static string in each view, so it cannot drift from what the
// key handlers actually do — and so it never advertises an action that would do
// nothing, such as interrupting an idle agent.
package footer

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/haha-systems/ghost/internal/ui/theme"
)

// Screen names the screen the operator is on.
type Screen int

const (
	Dashboard Screen = iota
	AgentDetail
	Epistemic
)

// Focus names the region holding keyboard input.
type Focus int

const (
	Roster Focus = iota
	Stream
	Decisions
	Activity
	Steering
	InspectorOverview
	InspectorDetail
)

// Context is everything the footer needs to know.
//
// The PRD sketches an AgentState field; the only decision it drives is whether
// interruption is possible, so that judgement is made by the caller and arrives
// here already resolved as CanInterrupt.
type Context struct {
	Screen Screen
	Focus  Focus

	// CanInterrupt reports that a turn is running and can be stopped.
	CanInterrupt bool
	// Following reports that the focused pane is pinned to live output.
	Following bool
	// HasDraft reports unsent text in the steering editor.
	HasDraft bool
	// SubmitKey is the configured steering submit binding.
	SubmitKey string
}

// Hint is one key and the action it performs.
type Hint struct {
	Key   string
	Label string
}

// Hints returns the actions available in a context, most useful first.
func Hints(ctx Context) []Hint {
	if ctx.Screen == Epistemic {
		if ctx.Focus == InspectorDetail {
			return []Hint{{"↑↓", "SCROLL"}, {"PGUP/PGDN", "PAGE"}, {"ESC", "BACK"}}
		}
		return []Hint{{"↑↓", "SELECT"}, {"ENTER", "DETAIL"}, {"PGUP/PGDN", "SCROLL"}, {"ESC", "BACK"}}
	}
	switch ctx.Focus {
	case Steering:
		return steeringHints(ctx)
	case Roster:
		return []Hint{
			{"↑↓", "SELECT"},
			{"ENTER", "OPEN"},
			{"E", "INSPECT"},
			{"TAB", "EVENTS"},
			{"?", "HELP"},
			{"Q", "QUIT"},
		}
	default:
		return paneHints(ctx)
	}
}

// paneHints covers every scrolling log pane. END is offered only while the pane
// has actually stopped following, and HOME only while it has not reached the
// oldest entry it could show.
func paneHints(ctx Context) []Hint {
	hints := []Hint{
		{"↑↓", "SCROLL"},
		{"PGUP/PGDN", "PAGE"},
	}
	if ctx.Following {
		hints = append(hints, Hint{"HOME", "TOP"})
	} else {
		hints = append(hints, Hint{"END", "FOLLOW"})
	}
	hints = append(hints, Hint{"TAB", nextRegion(ctx)})
	if ctx.Screen == AgentDetail {
		return append(hints, Hint{"ESC", "BACK"})
	}
	return append(hints, Hint{"E", "INSPECT"}, Hint{"?", "HELP"})
}

func steeringHints(ctx Context) []Hint {
	hints := []Hint{{submitLabel(ctx.SubmitKey), "SEND"}, {"TAB", nextRegion(ctx)}}
	if ctx.CanInterrupt {
		hints = append(hints, Hint{"CTRL+X", "INTERRUPT"})
	}
	if ctx.HasDraft {
		hints = append(hints, Hint{"ESC", "CLEAR"})
	} else if ctx.Screen == AgentDetail {
		hints = append(hints, Hint{"ESC", "BACK"})
	}
	if ctx.Screen == Dashboard {
		hints = append(hints, Hint{"?", "HELP"})
	}
	return hints
}

// nextRegion names where Tab goes, so the footer teaches the cycle rather than
// just asserting that Tab does something.
func nextRegion(ctx Context) string {
	if ctx.Screen == Dashboard {
		switch ctx.Focus {
		case Roster:
			return "EVENTS"
		case Stream:
			return "STEER"
		default:
			return "ROSTER"
		}
	}
	switch ctx.Focus {
	case Decisions:
		return "ACTIVITY"
	case Activity:
		return "STEER"
	default:
		return "DECISIONS"
	}
}

func submitLabel(submitKey string) string {
	if submitKey == "enter" {
		return "ENTER"
	}
	return "CTRL+ENTER"
}

// Render lays the hints out on one row, dropping whole hints from the end
// rather than truncating a key when the terminal is narrow.
func Render(th theme.Theme, hints []Hint, width int) string {
	if width <= 0 || len(hints) == 0 {
		return ""
	}
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Colors.Accent)).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Colors.TextMuted))

	const separator = "   "
	var row string
	for i, hint := range hints {
		piece := keyStyle.Render(hint.Key) + " " + labelStyle.Render(hint.Label)
		candidate := piece
		if i > 0 {
			candidate = row + separator + piece
		}
		if ansi.StringWidth(candidate) > width {
			break
		}
		row = candidate
	}
	if gap := width - ansi.StringWidth(row); gap > 0 {
		row += strings.Repeat(" ", gap)
	}
	return row
}
