package agentdetail

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

func TestLiveLogFitsTheDetailViewport(t *testing.T) {
	m := New(theme.Bloodwire(), keymap.Default()).ClearLogs().SetSize(100, 24)
	for i := 0; i < 40; i++ {
		m = m.AddLog(fmt.Sprintf("line %d", i))
	}
	if lines := strings.Count(m.View(), "\n") + 1; lines > 24 {
		t.Fatalf("detail view has %d lines, want at most 24", lines)
	}
}

func TestLiveLogScrollsWhenFocused(t *testing.T) {
	m := New(theme.Bloodwire(), keymap.Default()).ClearLogs().SetSize(100, 24)
	for i := 0; i < 40; i++ {
		m = m.AddLog(fmt.Sprintf("line %d", i))
	}
	if strings.Contains(m.View(), "line 0") {
		t.Fatal("log did not start at the newest activity")
	}
	// Tab walks steering → decisions → activity; untyped entries are activity.
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Text: "tab"}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Text: "tab"}))
	if m.Focus() != FocusActivity {
		t.Fatalf("focus = %v, want activity", m.Focus())
	}
	for i := 0; i < 40; i++ {
		m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Text: "up"}))
	}
	if !strings.Contains(m.View(), "line 0") {
		t.Fatal("log did not scroll to earlier activity")
	}
}

func TestLiveLogLabelsEventsAndResponses(t *testing.T) {
	m := New(theme.Bloodwire(), keymap.Default()).ClearLogs().SetSize(100, 24)
	m = m.AddLog("turn started").AppendLog("Hello. How may I help?")
	view := m.View()
	for _, want := range []string{"event", "turn started", "response", "Hello. How may I help?"} {
		if !strings.Contains(view, want) {
			t.Fatalf("live log missing %q", want)
		}
	}
}

func TestSteeringSubmitsModifiedEnterAndClearsOnCtrlC(t *testing.T) {
	m := New(theme.Bloodwire(), keymap.Default()).SetAgent(ghostmodel.Agent{ID: "veil"})
	for _, r := range "inspect" {
		m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatal("ctrl+enter did not submit")
	}
	if got := cmd().(SteeringSubmittedMsg).Text; got != "inspect" {
		t.Fatalf("text=%q", got)
	}
	for _, r := range "clear" {
		m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if got := m.input.Value(); got != "" {
		t.Fatalf("ctrl+c left input %q", got)
	}
}
