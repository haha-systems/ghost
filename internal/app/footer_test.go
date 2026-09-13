package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/ui/layout"
)

// The footer is always present, and says what the focused region can do.
func TestDashboardFooterFollowsFocus(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	if view := m.View().Content; !strings.Contains(view, "SELECT") || !strings.Contains(view, "OPEN") {
		t.Fatal("roster footer does not offer selection and opening")
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	if view := m.View().Content; !strings.Contains(view, "SCROLL") {
		t.Fatal("event stream footer does not offer scrolling")
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	if view := m.View().Content; !strings.Contains(view, "SEND") {
		t.Fatal("steering footer does not offer sending")
	}
}

func TestDetailFooterFollowsFocus(t *testing.T) {
	m := rosterModel(t)
	m = openAgent(t, m, "veil")

	if view := m.View().Content; !strings.Contains(view, "SEND") {
		t.Fatal("detail steering footer does not offer sending")
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	view := m.View().Content
	if !strings.Contains(view, "SCROLL") {
		t.Fatal("decisions footer does not offer scrolling")
	}
	if !strings.Contains(view, "BACK") {
		t.Fatal("detail footer does not offer leaving the screen")
	}
}

// An idle agent cannot be interrupted, so the footer must not say it can.
func TestFooterAdvertisesInterruptOnlyWhenPossible(t *testing.T) {
	m := rosterModel(t)
	m = openAgent(t, m, "veil")
	if strings.Contains(m.View().Content, "INTERRUPT") {
		t.Fatal("idle agent footer advertises interrupt")
	}
}

// The footer is budgeted, not painted over the layout: the frame still fits.
func TestFooterFitsWithinTheFrame(t *testing.T) {
	for _, size := range []struct{ w, h int }{
		{layout.MinWidth, layout.MinHeight}, {100, 30}, {120, 40}, {200, 60},
	} {
		m := New(config.Default(), nil)
		m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		for _, name := range []string{"dashboard", "detail"} {
			if name == "detail" {
				cmd := mustCmd(t, m, press("enter", tea.KeyEnter))
				m, _ = runCmd(t, m, cmd)
			}
			view := m.View().Content
			if lines := strings.Count(view, "\n") + 1; lines != size.h {
				t.Fatalf("%s at %dx%d rendered %d lines", name, size.w, size.h, lines)
			}
		}
	}
}

// A terminal too small for the layout shows guidance, not a broken frame.
func TestSmallTerminalStillHandledWithTheFooterReserved(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 18})
	view := m.View().Content
	if !strings.Contains(view, "Terminal too small") {
		t.Fatalf("small terminal did not render guidance: %q", view)
	}
}
