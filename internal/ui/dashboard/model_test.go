package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/layout"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

func newModel(agents ...ghostmodel.Agent) Model {
	if len(agents) == 0 {
		agents = ghostmodel.MockAgents()
	}
	return New(theme.Bloodwire(), keymap.Default(), agents, nil)
}

func press(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

// The view must fill its terminal exactly at every supported size; overflowing
// by even one row pushes content out of the frame.
func TestViewFitsEverySupportedSize(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {100, 30}, {120, 40}, {200, 60}}
	for _, s := range sizes {
		m := newModel().SetSize(s.w, s.h)
		for i := 0; i < 200; i++ {
			m = m.SetEvents(append(m.events, event.Event{
				Time: time.Now(), Source: "VEIL", Kind: event.KindCommand,
				Message: fmt.Sprintf("go test ./... run %d", i),
			}))
		}
		view := m.View()
		if h := lipgloss.Height(view); h != s.h {
			t.Fatalf("%dx%d: view height %d", s.w, s.h, h)
		}
		if w := lipgloss.Width(view); w != s.w {
			t.Fatalf("%dx%d: view width %d", s.w, s.h, w)
		}
	}
}

func TestNarrowTerminalShowsGuidance(t *testing.T) {
	m := newModel().SetSize(60, 20)
	if !strings.Contains(m.View(), "Terminal too small") {
		t.Fatal("narrow terminal did not render guidance")
	}
}

// The original defect: an idle steering editor claimed twenty rows and left the
// event stream with two.
func TestIdleSteeringLeavesTheLogItsColumn(t *testing.T) {
	m := newModel().SetSize(120, 32)
	if got := m.Screen().Steering; got != layout.MinSteeringRows {
		t.Fatalf("idle steering = %d rows, want %d", got, layout.MinSteeringRows)
	}
	if got := m.Screen().Panes[0]; got < 20 {
		t.Fatalf("event stream = %d rows, want most of the column", got)
	}
}

func TestSteeringGrowsWithContentAndShrinksBack(t *testing.T) {
	m := newModel().SetSize(120, 40)
	m, _ = m.Update(press(tea.KeyTab, "tab"))
	if m.Focus() != FocusSteering {
		t.Fatal("tab did not focus steering")
	}
	logBefore := m.Screen().Panes[0]

	for i := 0; i < 5; i++ {
		m, _ = m.Update(press('x', "x"))
		m, _ = m.Update(press(tea.KeyEnter, "enter"))
	}
	if m.Screen().Steering <= layout.MinSteeringRows {
		t.Fatalf("steering did not grow: %d rows", m.Screen().Steering)
	}
	if m.Screen().Panes[0] >= logBefore {
		t.Fatal("log did not yield rows to the grown editor")
	}
	if lipgloss.Height(m.View()) != 40 {
		t.Fatal("grown editor pushed the view out of the frame")
	}

	m, _ = m.Update(press('c', "c")) // ctrl+c clears the draft
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if m.Screen().Steering != layout.MinSteeringRows {
		t.Fatalf("steering did not shrink back: %d rows", m.Screen().Steering)
	}
}

func TestSteeringIsCappedAtTwentyRows(t *testing.T) {
	m := newModel().SetSize(120, 60)
	m, _ = m.Update(press(tea.KeyTab, "tab"))
	for i := 0; i < 40; i++ {
		m, _ = m.Update(press('x', "x"))
		m, _ = m.Update(press(tea.KeyEnter, "enter"))
	}
	if got := m.Screen().Steering; got != layout.MaxSteeringRows {
		t.Fatalf("steering = %d rows, want cap %d", got, layout.MaxSteeringRows)
	}
}

// Normalized kinds must reach the stream rather than collapsing to "agent".
func TestEventStreamShowsNormalizedKinds(t *testing.T) {
	m := newModel().SetSize(140, 40)
	m = m.SetEvents([]event.Event{
		{Time: time.Now(), Source: "VEIL", Kind: event.KindCommand, Message: "go build ./..."},
		{Time: time.Now(), Source: "VEIL", Kind: event.KindFile, Message: "read main.go"},
		{Time: time.Now(), Source: "VEIL", Kind: event.KindThinking, Message: "considering"},
	})
	view := m.View()
	for _, want := range []string{"command", "file", "thinking"} {
		if !strings.Contains(view, want) {
			t.Fatalf("event stream missing kind %q", want)
		}
	}
}

// A long or multi-line summary must be clipped to the pane, never reflow it.
func TestLongEventsDoNotBreakTheLayout(t *testing.T) {
	m := newModel().SetSize(100, 30)
	m = m.SetEvents([]event.Event{
		{Time: time.Now(), Source: "VEIL", Kind: event.KindResponse, Message: strings.Repeat("long ", 400)},
		{Time: time.Now(), Source: "VEIL", Kind: event.KindResponse, Message: "first\nsecond\nthird"},
	})
	view := m.View()
	if h := lipgloss.Height(view); h != 30 {
		t.Fatalf("view height %d, want 30", h)
	}
	for _, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Fatalf("line width %d exceeds terminal", w)
		}
	}
}

func TestSidebarShowsRosterAndGoalState(t *testing.T) {
	m := newModel().SetSize(120, 40).SetQAC(true, "analyst").SetWork("done", "ship the refactor")
	view := m.View()
	for _, want := range []string{"VEIL", "WRAITH", "ROSTER", "DONE", "ANALYST", "ship the refactor"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sidebar missing %q", want)
		}
	}
}

func TestHelpFooterStaysInsideTheFrame(t *testing.T) {
	m := newModel().SetSize(80, 24)
	m, _ = m.Update(press('?', "?"))
	if h := lipgloss.Height(m.View()); h != 24 {
		t.Fatalf("view height with help = %d, want 24", h)
	}
	if !strings.Contains(m.View(), "previous agent") {
		t.Fatal("help footer did not render")
	}
}

func TestSelectionWrapsAndOpensAgent(t *testing.T) {
	m := newModel().SetSize(120, 40)
	m, _ = m.Update(press(tea.KeyUp, "up"))
	if got := m.SelectedIndex(); got != 2 {
		t.Fatalf("selection = %d, want wrap to 2", got)
	}
	_, cmd := m.Update(press(tea.KeyEnter, "enter"))
	if cmd == nil {
		t.Fatal("enter did not emit an open message")
	}
	if msg, ok := cmd().(OpenAgentMsg); !ok || msg.Index != 2 {
		t.Fatalf("open message = %#v", cmd())
	}
}
