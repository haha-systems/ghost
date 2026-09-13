package dashboard

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/event"
)

func wheel(x, y int, up bool) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg(tea.Mouse{X: x, Y: y, Button: button})
}

// busy returns a sized dashboard holding more events than fit on screen.
func busy(t *testing.T) Model {
	t.Helper()
	m := newModel().SetSize(120, 40)
	// Each row must be distinguishable, or a scrolled viewport renders
	// identically and the test cannot tell whether it moved.
	for i := 0; i < 60; i++ {
		m = m.AppendEvent(event.Event{Source: "VEIL", Kind: event.KindCommand, Message: fmt.Sprintf("command %d", i)})
	}
	return m
}

// Roster → event stream → steering → roster, as the keyboard contract states.
func TestTabCyclesRosterStreamSteering(t *testing.T) {
	m := busy(t)
	want := []Focus{FocusEvents, FocusSteering, FocusAgents}
	for i, expect := range want {
		m, _ = m.Update(press(tea.KeyTab, "tab"))
		if m.Focus() != expect {
			t.Fatalf("tab %d gave focus %v, want %v", i+1, m.Focus(), expect)
		}
	}
}

func TestShiftTabCyclesBackwards(t *testing.T) {
	m := busy(t)
	want := []Focus{FocusSteering, FocusEvents, FocusAgents}
	for i, expect := range want {
		m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
		if m.Focus() != expect {
			t.Fatalf("shift+tab %d gave focus %v, want %v", i+1, m.Focus(), expect)
		}
	}
}

func TestArrowsScrollTheFocusedStream(t *testing.T) {
	m := busy(t)
	m, _ = m.Update(press(tea.KeyTab, "tab"))
	if m.Focus() != FocusEvents {
		t.Fatalf("focus = %v, want event stream", m.Focus())
	}
	before := m.stream.View()
	m, _ = m.Update(press(tea.KeyUp, "up"))
	if m.stream.View() == before {
		t.Fatal("up arrow did not scroll the event stream")
	}
	if m.stream.Follow() {
		t.Fatal("scrolling up left the stream following")
	}
	m, _ = m.Update(press(tea.KeyEnd, "end"))
	if !m.stream.Follow() {
		t.Fatal("end did not resume follow")
	}
}

// The roster keeps the arrow keys while it holds focus.
func TestArrowsSelectAgentsWhenRosterFocused(t *testing.T) {
	m := busy(t)
	before := m.SelectedIndex()
	m, _ = m.Update(press(tea.KeyDown, "down"))
	if m.SelectedIndex() == before {
		t.Fatal("down arrow did not move the roster selection")
	}
	if !m.stream.Follow() {
		t.Fatal("roster navigation disturbed the event stream")
	}
}

func TestWheelScrollsTheStreamUnderThePointer(t *testing.T) {
	m := busy(t)
	bounds := m.Screen().PaneBounds()
	if len(bounds) != 1 {
		t.Fatalf("got %d panes, want 1", len(bounds))
	}
	x, y := bounds[0].X+bounds[0].Width/2, bounds[0].Y+bounds[0].Height/2
	before := m.stream.View()
	m, _ = m.Update(wheel(x, y, true))
	if m.stream.View() == before {
		t.Fatal("wheel over the stream did not scroll it")
	}
	if m.Focus() != FocusAgents {
		t.Fatalf("wheel moved focus to %v", m.Focus())
	}
}

func TestWheelOverTheSidebarIsIgnored(t *testing.T) {
	m := busy(t)
	before := m.stream.View()
	m, _ = m.Update(wheel(0, 0, true))
	if m.stream.View() != before {
		t.Fatal("a wheel event over the sidebar scrolled the stream")
	}
}

// A detached stream must not be dragged down by arriving events.
func TestArrivingEventsDoNotMoveADetachedStream(t *testing.T) {
	m := busy(t)
	m, _ = m.Update(press(tea.KeyTab, "tab"))
	for i := 0; i < 4; i++ {
		m, _ = m.Update(press(tea.KeyUp, "up"))
	}
	before := m.stream.View()
	for i := 0; i < 5; i++ {
		m = m.AppendEvent(event.Event{Source: "WRAITH", Kind: event.KindCommand, Message: fmt.Sprintf("late %d", i)})
	}
	if m.stream.View() != before {
		t.Fatal("arriving events moved a detached stream")
	}
	if m.stream.NewCount() != 5 {
		t.Fatalf("new count = %d, want 5", m.stream.NewCount())
	}
}
