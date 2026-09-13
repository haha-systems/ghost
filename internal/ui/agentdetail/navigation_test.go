package agentdetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/ui/layout"
)

// wheel builds a scroll event at a terminal cell.
func wheel(x, y int, up bool) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg(tea.Mouse{X: x, Y: y, Button: button})
}

// centreOf returns a cell inside a pane's body.
func centreOf(b layout.Bounds) (int, int) {
	return b.X + b.Width/2, b.Y + b.Height/2
}

func TestTabCyclesThroughEveryRegion(t *testing.T) {
	m := populated(t)
	want := []Focus{FocusDecisions, FocusActivity, FocusSteering, FocusDecisions}
	for i, expect := range want {
		m, _ = m.Update(press(tea.KeyTab, "tab"))
		if m.Focus() != expect {
			t.Fatalf("tab %d gave focus %v, want %v", i+1, m.Focus(), expect)
		}
	}
}

func TestShiftTabCyclesBackwards(t *testing.T) {
	m := populated(t)
	want := []Focus{FocusActivity, FocusDecisions, FocusSteering, FocusActivity}
	for i, expect := range want {
		m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
		if m.Focus() != expect {
			t.Fatalf("shift+tab %d gave focus %v, want %v", i+1, m.Focus(), expect)
		}
	}
}

func TestArrowKeysScrollTheFocusedPane(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusDecisions)
	before := m.decisions.View()
	activityBefore := m.activity.View()
	m, _ = m.Update(press(tea.KeyUp, "up"))
	if m.decisions.View() == before {
		t.Fatal("up arrow did not scroll the focused pane")
	}
	if m.activity.View() != activityBefore || !m.activity.Follow() {
		t.Fatal("the unfocused pane was affected")
	}
}

func TestPageKeysScrollTheFocusedPane(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusActivity)
	before := m.activity.View()
	m, _ = m.Update(press(tea.KeyPgUp, "pgup"))
	if m.activity.View() == before {
		t.Fatal("page up did not scroll the focused pane")
	}
	if m.activity.Follow() {
		t.Fatal("page up left the pane following")
	}
	m, _ = m.Update(press(tea.KeyPgDown, "pgdown"))
	m, _ = m.Update(press(tea.KeyPgDown, "pgdown"))
	if !m.activity.Follow() {
		t.Fatal("paging back down did not resume follow")
	}
}

func TestHomeAndEndJumpToTheEndsOfHistory(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusDecisions)
	m, _ = m.Update(press(tea.KeyHome, "home"))
	if !strings.Contains(m.decisions.View(), "decision 0") {
		t.Fatal("home did not jump to the oldest entry")
	}
	if m.decisions.Follow() {
		t.Fatal("home left the pane following")
	}
	m, _ = m.Update(press(tea.KeyEnd, "end"))
	if !strings.Contains(m.decisions.View(), "decision 39") {
		t.Fatal("end did not jump to the newest entry")
	}
	if !m.decisions.Follow() {
		t.Fatal("end did not resume follow")
	}
}

// End is the documented way back to live output, and clears the indicator.
func TestEndResumesFollowAndClearsTheCounter(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusActivity)
	m, _ = m.Update(press(tea.KeyUp, "up"))
	m = m.AppendEntry(entryOfKind("command", "late activity"))
	if m.activity.NewCount() == 0 {
		t.Fatal("precondition: expected a counted arrival")
	}
	m, _ = m.Update(press(tea.KeyEnd, "end"))
	if !m.activity.Follow() || m.activity.NewCount() != 0 {
		t.Fatalf("end left follow=%v count=%d", m.activity.Follow(), m.activity.NewCount())
	}
}

// Steering owns its own cursor keys; the panes must not take them.
func TestSteeringKeepsItsArrowKeys(t *testing.T) {
	m := populated(t)
	if m.Focus() != FocusSteering {
		t.Fatalf("focus = %v, want steering", m.Focus())
	}
	decisions, activity := m.decisions.View(), m.activity.View()
	for _, k := range []rune{tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight} {
		m, _ = m.Update(press(k, ""))
	}
	if m.decisions.View() != decisions || m.activity.View() != activity {
		t.Fatal("arrow keys in the steering editor scrolled a log pane")
	}
}

func TestWheelScrollsThePaneUnderThePointer(t *testing.T) {
	m := populated(t)
	bounds := m.Screen().PaneBounds()
	if len(bounds) != 2 {
		t.Fatalf("got %d panes, want 2", len(bounds))
	}

	x, y := centreOf(bounds[0])
	before := m.decisions.View()
	m, _ = m.Update(wheel(x, y, true))
	if m.decisions.View() == before {
		t.Fatal("wheel over decisions did not scroll it")
	}
	if !m.activity.Follow() {
		t.Fatal("wheel over decisions disturbed activity")
	}

	x, y = centreOf(bounds[1])
	before = m.activity.View()
	m, _ = m.Update(wheel(x, y, true))
	if m.activity.View() == before {
		t.Fatal("wheel over activity did not scroll it")
	}
}

// Inspecting with the mouse must not move where the keyboard types.
func TestWheelDoesNotChangeKeyboardFocus(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusDecisions)
	bounds := m.Screen().PaneBounds()
	x, y := centreOf(bounds[1])
	m, _ = m.Update(wheel(x, y, true))
	if m.Focus() != FocusDecisions {
		t.Fatalf("wheel moved focus to %v", m.Focus())
	}
}

func TestWheelOutsideEveryPaneIsIgnored(t *testing.T) {
	m := populated(t)
	decisions, activity := m.decisions.View(), m.activity.View()
	m, _ = m.Update(wheel(0, 0, true))
	if m.decisions.View() != decisions || m.activity.View() != activity {
		t.Fatal("a wheel event over the sidebar scrolled a pane")
	}
}
