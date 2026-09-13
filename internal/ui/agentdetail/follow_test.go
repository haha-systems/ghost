package agentdetail

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/history"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

func press(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

// populated returns a detail screen holding plenty of both kinds of entry.
func populated(t *testing.T) Model {
	t.Helper()
	m := New(theme.Bloodwire(), keymap.Default()).
		SetAgent(ghostmodel.Agent{ID: "veil", Callsign: "VEIL"}).
		SetSize(120, 40)
	entries := make([]history.Entry, 0, 80)
	for i := 0; i < 40; i++ {
		entries = append(entries,
			history.Entry{AgentID: "veil", Kind: event.KindResponse, Message: fmt.Sprintf("decision %d", i)},
			history.Entry{AgentID: "veil", Kind: event.KindCommand, Message: fmt.Sprintf("activity %d", i)},
		)
	}
	return m.SetHistory(entries)
}

// focusPane tabs from steering to the named pane.
func focusPane(t *testing.T, m Model, want Focus) Model {
	t.Helper()
	for i := 0; i < 4; i++ {
		if m.Focus() == want {
			return m
		}
		m, _ = m.Update(press(tea.KeyTab, "tab"))
	}
	t.Fatalf("could not reach focus %v", want)
	return m
}

func TestPanesStartFollowing(t *testing.T) {
	m := populated(t)
	if !m.decisions.Follow() || !m.activity.Follow() {
		t.Fatal("a freshly hydrated screen is not following")
	}
}

// The defect this replaces: one shared follow flag meant reading back through
// decisions also stopped activity from following.
func TestScrollingDecisionsLeavesActivityFollowing(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusDecisions)
	for i := 0; i < 5; i++ {
		m, _ = m.Update(press(tea.KeyUp, "up"))
	}
	if m.decisions.Follow() {
		t.Fatal("scrolling decisions left it following")
	}
	if !m.activity.Follow() {
		t.Fatal("scrolling decisions also detached activity")
	}
}

func TestScrollingActivityLeavesDecisionsFollowing(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusActivity)
	for i := 0; i < 5; i++ {
		m, _ = m.Update(press(tea.KeyUp, "up"))
	}
	if m.activity.Follow() {
		t.Fatal("scrolling activity left it following")
	}
	if !m.decisions.Follow() {
		t.Fatal("scrolling activity also detached decisions")
	}
}

// Arriving entries are counted, not allowed to move a detached pane.
func TestDetachedPaneCountsArrivalsWithoutMoving(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusDecisions)
	for i := 0; i < 5; i++ {
		m, _ = m.Update(press(tea.KeyUp, "up"))
	}
	before := m.decisions.View()
	for i := 0; i < 3; i++ {
		m = m.AppendEntry(history.Entry{AgentID: "veil", Kind: event.KindResponse, Message: "late decision"})
	}
	if m.decisions.NewCount() != 3 {
		t.Fatalf("new count = %d, want 3", m.decisions.NewCount())
	}
	if m.decisions.View() != before {
		t.Fatal("a detached pane moved when entries arrived")
	}
}

// An arrival in one pane must not be reported by the other.
func TestArrivalsAreCountedByTheReceivingPaneOnly(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusDecisions)
	m, _ = m.Update(press(tea.KeyUp, "up"))
	m = focusPane(t, m, FocusActivity)
	m, _ = m.Update(press(tea.KeyUp, "up"))

	m = m.AppendEntry(history.Entry{AgentID: "veil", Kind: event.KindCommand, Message: "late activity"})
	if m.activity.NewCount() != 1 {
		t.Fatalf("activity new count = %d, want 1", m.activity.NewCount())
	}
	if m.decisions.NewCount() != 0 {
		t.Fatalf("decisions counted %d arrivals that were not its own", m.decisions.NewCount())
	}
}

func TestReachingTheBottomResumesFollowAndClearsTheIndicator(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusActivity)
	for i := 0; i < 5; i++ {
		m, _ = m.Update(press(tea.KeyUp, "up"))
	}
	m = m.AppendEntry(history.Entry{AgentID: "veil", Kind: event.KindCommand, Message: "late activity"})
	if m.activity.NewCount() == 0 {
		t.Fatal("detached pane did not count an arrival")
	}
	m.activity.GotoBottom()
	if !m.activity.Follow() {
		t.Fatal("returning to the bottom did not resume follow")
	}
	if m.activity.NewCount() != 0 {
		t.Fatalf("resuming left %d outstanding arrivals", m.activity.NewCount())
	}
}

// The indicator is only rendered for a pane that has stopped following.
func TestNewIndicatorAppearsOnlyWhileDetached(t *testing.T) {
	m := populated(t)
	if strings.Contains(m.View(), "NEW") {
		t.Fatal("a following screen advertises new entries")
	}
	m = focusPane(t, m, FocusActivity)
	for i := 0; i < 5; i++ {
		m, _ = m.Update(press(tea.KeyUp, "up"))
	}
	m = m.AppendEntry(history.Entry{AgentID: "veil", Kind: event.KindCommand, Message: "late activity"})
	if !strings.Contains(m.View(), "1 NEW") {
		t.Fatal("detached pane does not show the new-entry indicator")
	}
	m.activity.GotoBottom()
	if strings.Contains(m.View(), "NEW") {
		t.Fatal("indicator survived resuming follow")
	}
}

// Switching agents must not carry one agent's scroll position into another's.
func TestOpeningAnotherAgentStartsAtLiveOutput(t *testing.T) {
	m := populated(t)
	m = focusPane(t, m, FocusActivity)
	for i := 0; i < 5; i++ {
		m, _ = m.Update(press(tea.KeyUp, "up"))
	}
	if m.activity.Follow() {
		t.Fatal("precondition: activity should be detached")
	}
	m = m.SetAgent(ghostmodel.Agent{ID: "wraith", Callsign: "WRAITH"}).
		SetHistory([]history.Entry{{AgentID: "wraith", Kind: event.KindCommand, Message: "wraith work"}})
	if !m.activity.Follow() || !m.decisions.Follow() {
		t.Fatal("a newly opened agent inherited a detached scroll position")
	}
}
