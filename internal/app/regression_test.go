package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/ui/agentdetail"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
)

func agentSteering(agentID, text string) agentdetail.SteeringSubmittedMsg {
	return agentdetail.SteeringSubmittedMsg{AgentID: agentID, Text: text}
}

// A draft must survive unrelated traffic. Losing half-typed steering because an
// agent happened to emit an event would be the worst kind of interruption.
func TestSteeringDraftSurvivesArrivingEvents(t *testing.T) {
	m := rosterModel(t)
	// Roster -> event stream -> steering.
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	for _, r := range "investigate" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	if got := m.dashboard.InputValue(); got != "investigate" {
		t.Fatalf("draft = %q before events arrived", got)
	}

	m = emit(t, m, "veil", runtime.KindCommand, "go test ./...")
	m = emit(t, m, "wraith", runtime.KindMessage, "a response")
	m, _ = updateModel(t, m, tickMsg(timeNow()))

	if got := m.dashboard.InputValue(); got != "investigate" {
		t.Fatalf("draft = %q after events arrived", got)
	}
	if !m.InputFocused() {
		t.Fatal("focus left the steering editor when events arrived")
	}
}

// A resize must not lose a draft either.
func TestSteeringDraftSurvivesResize(t *testing.T) {
	m := rosterModel(t)
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	for _, r := range "resize" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 28})
	if got := m.dashboard.InputValue(); got != "resize" {
		t.Fatalf("draft = %q after resize", got)
	}
}

// Unrelated traffic must not move an operator who has scrolled back.
func TestDetachedStreamIsNotMovedByUnrelatedTraffic(t *testing.T) {
	m := rosterModel(t)
	for i := 0; i < 40; i++ {
		m = emit(t, m, "veil", runtime.KindCommand, "command "+string(rune('a'+i%26)))
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	for i := 0; i < 4; i++ {
		m, _ = updateModel(t, m, press("up", tea.KeyUp))
	}
	m, _ = updateModel(t, m, tickMsg(timeNow()))
	m = emit(t, m, "wraith", runtime.KindTool, "a tool call")

	if !strings.Contains(m.dashboard.View(), "NEW") {
		t.Fatal("detached stream did not report the new entry")
	}
}

// Opening an agent, leaving, and returning must not disturb the roster.
func TestNavigatingDoesNotDisturbTheRoster(t *testing.T) {
	m := rosterModel(t)
	before := m.dashboard.SelectedIndex()
	m, _ = updateModel(t, m, dashboard.OpenAgentMsg{Index: before})
	// Leaving the detail screen is a command, not an immediate transition.
	cmd := mustCmd(t, m, press("esc", tea.KeyEscape))
	m, _ = runCmd(t, m, cmd)
	if got := m.dashboard.SelectedIndex(); got != before {
		t.Fatalf("selection moved from %d to %d", before, got)
	}
	if m.Screen() != DashboardScreen {
		t.Fatalf("screen = %v, want dashboard", m.Screen())
	}
}

// Per-agent steering still reaches the agent it names.
func TestPerAgentSteeringStillTargetsItsAgent(t *testing.T) {
	m := rosterModel(t)
	m = openAgent(t, m, "veil")
	before := m.EventCount()
	m, cmd := updateModel(t, m, agentSteering("veil", "look again"))
	if cmd != nil {
		m, _ = runCmd(t, m, cmd)
	}
	if m.EventCount() <= before {
		t.Fatal("per-agent steering produced no event")
	}
	found := false
	for _, e := range m.history.Agent("veil") {
		if strings.Contains(e.Message, "look again") {
			found = true
		}
	}
	if !found {
		t.Fatal("per-agent steering did not reach the agent's history")
	}
}
