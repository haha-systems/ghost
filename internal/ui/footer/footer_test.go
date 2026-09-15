package footer

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/haha-systems/ghost/internal/ui/theme"
)

func lipglossWidth(s string) int { return ansi.StringWidth(s) }

func stripANSI(s string) string { return ansi.Strip(s) }

func keys(hints []Hint) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, h.Key+" "+h.Label)
	}
	return strings.Join(parts, "   ")
}

func has(hints []Hint, label string) bool {
	for _, h := range hints {
		if h.Label == label {
			return true
		}
	}
	return false
}

func TestRosterOffersSelectionAndOpening(t *testing.T) {
	got := Hints(Context{Screen: Dashboard, Focus: Roster})
	for _, want := range []string{"SELECT", "OPEN"} {
		if !has(got, want) {
			t.Fatalf("roster hints missing %q: %s", want, keys(got))
		}
	}
}

func TestSteeringOffersSend(t *testing.T) {
	got := Hints(Context{Screen: Dashboard, Focus: Steering})
	if !has(got, "SEND") {
		t.Fatalf("steering hints missing SEND: %s", keys(got))
	}
}

func TestSubmitHintFollowsTheConfiguredKey(t *testing.T) {
	ctrl := Hints(Context{Screen: Dashboard, Focus: Steering, SubmitKey: "ctrl_enter"})
	if ctrl[0].Key != "CTRL+ENTER" {
		t.Fatalf("submit key = %q, want CTRL+ENTER", ctrl[0].Key)
	}
	plain := Hints(Context{Screen: Dashboard, Focus: Steering, SubmitKey: "enter"})
	if plain[0].Key != "ENTER" {
		t.Fatalf("submit key = %q, want ENTER", plain[0].Key)
	}
}

// An action that cannot succeed must not be advertised.
func TestInterruptIsOfferedOnlyWhileATurnIsRunning(t *testing.T) {
	running := Hints(Context{Screen: AgentDetail, Focus: Steering, CanInterrupt: true})
	if !has(running, "INTERRUPT") {
		t.Fatalf("running agent hints missing INTERRUPT: %s", keys(running))
	}
	idle := Hints(Context{Screen: AgentDetail, Focus: Steering})
	if has(idle, "INTERRUPT") {
		t.Fatalf("idle agent advertises INTERRUPT: %s", keys(idle))
	}
}

func TestFollowHintAppearsOnlyWhileDetached(t *testing.T) {
	detached := Hints(Context{Screen: Dashboard, Focus: Stream})
	if !has(detached, "FOLLOW") {
		t.Fatalf("detached pane hints missing FOLLOW: %s", keys(detached))
	}
	following := Hints(Context{Screen: Dashboard, Focus: Stream, Following: true})
	if has(following, "FOLLOW") {
		t.Fatalf("following pane advertises FOLLOW: %s", keys(following))
	}
}

func TestScrollingHintsAppearForEveryPane(t *testing.T) {
	for _, focus := range []Focus{Stream, Decisions, Activity} {
		got := Hints(Context{Focus: focus})
		if !has(got, "SCROLL") || !has(got, "PAGE") {
			t.Fatalf("focus %v missing scroll hints: %s", focus, keys(got))
		}
	}
}

// The footer teaches the focus cycle rather than just asserting Tab exists.
func TestTabHintNamesTheNextRegion(t *testing.T) {
	cases := []struct {
		ctx  Context
		want string
	}{
		{Context{Screen: Dashboard, Focus: Roster}, "EVENTS"},
		{Context{Screen: Dashboard, Focus: Stream}, "STEER"},
		{Context{Screen: Dashboard, Focus: Steering}, "ROSTER"},
		{Context{Screen: AgentDetail, Focus: Decisions}, "ACTIVITY"},
		{Context{Screen: AgentDetail, Focus: Activity}, "STEER"},
		{Context{Screen: AgentDetail, Focus: Steering}, "DECISIONS"},
	}
	for _, c := range cases {
		got := Hints(c.ctx)
		if !has(got, c.want) {
			t.Fatalf("screen %v focus %v: missing tab target %q: %s",
				c.ctx.Screen, c.ctx.Focus, c.want, keys(got))
		}
	}
}

func TestEscapeClearsADraftAndOtherwiseLeaves(t *testing.T) {
	draft := Hints(Context{Screen: AgentDetail, Focus: Steering, HasDraft: true})
	if !has(draft, "CLEAR") {
		t.Fatalf("draft hints missing CLEAR: %s", keys(draft))
	}
	empty := Hints(Context{Screen: AgentDetail, Focus: Steering})
	if !has(empty, "BACK") {
		t.Fatalf("empty steering hints missing BACK: %s", keys(empty))
	}
}

func TestDetailPanesOfferBack(t *testing.T) {
	got := Hints(Context{Screen: AgentDetail, Focus: Activity})
	if !has(got, "BACK") {
		t.Fatalf("detail pane hints missing BACK: %s", keys(got))
	}
}

func TestRenderFitsTheGivenWidth(t *testing.T) {
	th := theme.Bloodwire()
	hints := Hints(Context{Screen: Dashboard, Focus: Stream})
	for _, width := range []int{20, 40, 80, 120} {
		row := Render(th, hints, width)
		if got := lipglossWidth(row); got != width {
			t.Fatalf("width %d produced a row of %d cells", width, got)
		}
		if strings.Contains(row, "\n") {
			t.Fatalf("width %d produced more than one row", width)
		}
	}
}

// A narrow terminal drops whole hints rather than cutting a key in half.
func TestNarrowFooterDropsWholeHints(t *testing.T) {
	th := theme.Bloodwire()
	hints := Hints(Context{Screen: Dashboard, Focus: Stream})
	row := stripANSI(Render(th, hints, 18))
	if strings.Contains(row, "PGUP/PG ") || strings.Contains(row, "SCROL ") {
		t.Fatalf("narrow footer truncated a hint: %q", row)
	}
}

func TestRenderHandlesNoRoom(t *testing.T) {
	if got := Render(theme.Bloodwire(), Hints(Context{}), 0); got != "" {
		t.Fatalf("zero width produced %q", got)
	}
}

func TestEpistemicFooterIsReadOnlyAndShowsNavigation(t *testing.T) {
	got := Hints(Context{Screen: Epistemic, Focus: InspectorOverview})
	joined := strings.ToUpper(keys(got))
	for _, want := range []string{"SELECT", "DETAIL", "SCROLL", "BACK"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("epistemic footer missing %q: %s", want, joined)
		}
	}
	for _, forbidden := range []string{"EDIT", "DELETE", "MUTATE", "RESOLVE"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("read-only footer exposes %q", forbidden)
		}
	}
}
