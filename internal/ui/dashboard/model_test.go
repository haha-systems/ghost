package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/haha-systems/ghost/internal/epistemic"
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

func TestAppendEventRendersOnlyTheNewLine(t *testing.T) {
	m := newModel().SetSize(120, 40)
	m = m.SetEvents([]event.Event{{Time: time.Now(), Source: "VEIL", Kind: event.KindResponse, Message: "first"}})
	firstLine := m.rendered[0]
	m.events[0].Message = "changed"
	before := len(m.rendered)
	m = m.AppendEvent(event.Event{Time: time.Now(), Source: "WRAITH", Kind: event.KindCommand, Message: "second"})
	if len(m.rendered) != before+1 {
		t.Fatalf("rendered lines = %d, want %d", len(m.rendered), before+1)
	}
	if m.rendered[0] != firstLine {
		t.Fatal("append re-rendered a retained event")
	}
	if !strings.Contains(m.rendered[len(m.rendered)-1], "second") {
		t.Fatal("append did not render the new event")
	}
}

func TestRelayoutKeepsEventRowsWhenWidthIsUnchanged(t *testing.T) {
	m := newModel().SetSize(120, 40)
	m = m.SetEvents([]event.Event{{Time: time.Now(), Source: "VEIL", Kind: event.KindResponse, Message: "first"}})
	m.rendered[0] = "cached row"
	m = m.SetSize(120, 30)
	if m.rendered[0] != "cached row" {
		t.Fatalf("row = %q, want cached row", m.rendered[0])
	}
}

func TestAppendEventRetainsTheMostRecentEvents(t *testing.T) {
	m := newModel().SetSize(120, 40)
	for i := 0; i < 2001; i++ {
		m = m.AppendEvent(event.Event{Time: time.Now(), Source: "VEIL", Kind: event.KindCommand, Message: fmt.Sprintf("event %d", i)})
	}
	if got := len(m.events); got != 2000 {
		t.Fatalf("events = %d, want 2000", got)
	}
	if !strings.Contains(m.events[0].Message, "event 1") {
		t.Fatalf("oldest retained event = %q, want event 1", m.events[0].Message)
	}
	if got := len(m.rendered); got != 2000 {
		t.Fatalf("rendered lines = %d, want 2000", got)
	}
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
	// Focus cycles roster -> event stream -> steering.
	m, _ = m.Update(press(tea.KeyTab, "tab"))
	m, _ = m.Update(press(tea.KeyTab, "tab"))
	if m.Focus() != FocusSteering {
		t.Fatal("two tabs did not focus steering")
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
	for _, want := range []string{"VEIL", "WRAITH", "ROSTER", "ACTIVE", "ANALYST", "ship the refactor"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sidebar missing %q", want)
		}
	}
}

func TestOperatorViewSeparatesWorkPhaseCognitionAndFrontier(t *testing.T) {
	view := epistemic.OperatorView{
		Goal: "Fix prompt plumbing", WorkStatus: epistemic.WorkActive, Phase: epistemic.PhaseAbduce,
		LeadingHypothesis: &epistemic.ObjectSummary{Label: "prompt lost before runtime", Status: "leading"},
		OpenUnknownCount:  2,
	}
	m := newModel().SetSize(120, 40).SetOperatorView(&view, "SHADE")
	out := m.View()
	for _, want := range []string{
		"WORK", "ACTIVE", "PHASE", "ABDUCE", "COGNITION", "SHADE", "Fix prompt plumbing",
		"TRIAGE", "FRAME", "EXECUTE", "CLOSE", "prompt lost", "UNKNOWN", "2", "CONTRA", "0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard missing %q", want)
		}
	}
	if strings.Contains(out, "ACTIVE / SHADE") {
		t.Fatal("WORK and COGNITION were combined")
	}
}

func TestReopenShowsBackendRouteReasonAndCount(t *testing.T) {
	view := epistemic.OperatorView{
		WorkStatus: epistemic.WorkActive, Phase: epistemic.PhaseAbduce, ReopenCount: 1,
		LastTransition: &epistemic.TransitionSummary{
			From: epistemic.PhaseExecute, To: epistemic.PhaseAbduce,
			Reason: "runtime capture contradicts leading hypothesis", TargetKind: epistemic.ObjectKindHypothesis,
		},
	}
	out := newModel().SetSize(120, 40).SetOperatorView(&view, "SHADE").View()
	clean := ansi.Strip(out)
	for _, want := range []string{"REOPEN", "1", "EXECUTE -> ABDUCE", "runtime capture"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("reopen view missing %q", want)
		}
	}
}

func TestCESUpdatePreservesFocusSteeringAndDetachedEventPane(t *testing.T) {
	m := newModel().SetSize(120, 40)
	for i := 0; i < 80; i++ {
		m = m.AppendEvent(event.Event{Source: "CES", Kind: event.KindPhase, Message: fmt.Sprintf("phase %d", i)})
	}
	m.stream.ScrollUp(5)
	if m.stream.Follow() {
		t.Fatal("test setup did not detach the event pane")
	}
	m, _ = m.Update(press(tea.KeyTab, "tab"))
	m, _ = m.Update(press(tea.KeyTab, "tab"))
	m, _ = m.Update(press('x', "x"))
	focus, draft := m.Focus(), m.InputValue()
	view := epistemic.OperatorView{WorkStatus: epistemic.WorkActive, Phase: epistemic.PhaseTriage}
	m = m.SetOperatorView(&view, "WRAITH")
	if m.Focus() != focus || m.InputValue() != draft {
		t.Fatal("CES update changed focus or steering draft")
	}
	if m.stream.Follow() {
		t.Fatal("CES update resumed event following")
	}
}

func TestSemanticReopenEventShowsRouteReasonAndCount(t *testing.T) {
	m := newModel().SetSize(140, 40).SetEvents([]event.Event{{
		Source: "CES", Kind: event.KindReopen, Message: "work reopened",
		Metadata: map[string]string{"from": "execute", "to": "abduce", "reason": "runtime capture contradicts leading hypothesis", "reopen_count": "1", "reopen_limit": "3"},
	}})
	out := strings.ToUpper(m.View())
	for _, want := range []string{"EXECUTE -> ABDUCE", "REOPEN 1/3", "RUNTIME CAPTURE CONTRADICTS LEADING HYPOTHESIS"} {
		if !strings.Contains(out, want) {
			t.Fatalf("semantic event missing %q:\n%s", want, ansi.Strip(m.View()))
		}
	}
}

func TestSemanticEventsNamePhaseHypothesisFrameActionAndVerification(t *testing.T) {
	m := newModel().SetSize(140, 40).SetEvents([]event.Event{
		{Kind: event.KindPhase, Message: "phase changed", Metadata: map[string]string{"from": "triage", "to": "abduce"}},
		{Kind: event.KindHypothesis, Metadata: map[string]string{"status": "leading", "summary": "prompt lost before runtime"}},
		{Kind: event.KindReject, Message: "adapter drops prompt"},
		{Kind: event.KindFrame, Message: "propagate instructions"},
		{Kind: event.KindAction, Message: "ran go test ./..."},
		{Kind: event.KindVerify, Message: "all tests passed"},
	})
	out := strings.ToLower(ansi.Strip(m.View()))
	for _, want := range []string{"triage -> abduce", "leading: prompt lost before runtime", "hypothesis rejected: adapter drops prompt", "frame activated: propagate instructions", "action: ran go test", "verification: all tests passed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("semantic event rendering missing %q:\n%s", want, out)
		}
	}
}

func TestContradictionNamesItsInvalidatedPhaseAndDiffersFromRuntimeError(t *testing.T) {
	view := epistemic.OperatorView{WorkStatus: epistemic.WorkActive, Phase: epistemic.PhaseAbduce}
	m := newModel().SetSize(120, 40).SetOperatorView(&view, "SHADE").SetOperatorSupplement(epistemic.OperatorSupplement{InvalidatedPhases: []epistemic.Phase{epistemic.PhaseFrame}})
	m = m.AppendEvent(event.Event{Source: "CES", Kind: event.KindContradict, Metadata: map[string]string{
		"target_kind": "frame", "source_alias": "O31", "target_alias": "H1",
		"reason": "runtime capture contains the configured prompt",
	}})
	clean := ansi.Strip(m.View())
	for _, want := range []string{"! FRAME", "FRAME INVALIDATED", "O31 contradicts H1", "runtime capture contains the configured prompt"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("contradiction view missing %q", want)
		}
	}
	if kindColour(m.theme, event.KindContradict) == kindColour(m.theme, event.KindError) {
		t.Fatal("epistemic contradiction has the same emphasis as an operational error")
	}
}

func TestCompleteAndIncompleteEventsHaveDifferentLabels(t *testing.T) {
	m := newModel().SetSize(140, 40).SetEvents([]event.Event{
		{Source: "CES", Kind: event.KindComplete, Message: "goal closed"},
		{Source: "CES", Kind: event.KindIncomplete, Message: "reopen budget exhausted"},
	})
	out := strings.ToUpper(m.View())
	if !strings.Contains(out, "COMPLETE") || !strings.Contains(out, "INCOMPLETE") {
		t.Fatal("terminal outcomes were not distinct in the event stream")
	}
}

func TestIncompleteWorkShowsItsTerminalReasonAndResidualFrontier(t *testing.T) {
	view := epistemic.OperatorView{
		WorkStatus: epistemic.WorkIncomplete, Phase: epistemic.PhaseAbduce,
		OpenUnknownCount: 2,
	}
	clean := ansi.Strip(newModel().SetSize(120, 40).SetOperatorView(&view, "SHADE").SetOperatorSupplement(epistemic.OperatorSupplement{TerminalReason: "reopen budget exhausted"}).View())
	for _, want := range []string{"INCOMPLETE", "REASON", "REOPEN BUDGET", "EXHAUSTED", "UNKNOWN", "2"} {
		if !strings.Contains(strings.ToUpper(clean), strings.ToUpper(want)) {
			t.Fatalf("incomplete work missing %q", want)
		}
	}
}

func TestSmallIncompleteViewKeepsTerminalReasonAndResidualFrontier(t *testing.T) {
	view := epistemic.OperatorView{
		WorkStatus: epistemic.WorkIncomplete, Phase: epistemic.PhaseAbduce,
		OpenUnknownCount: 2, ContradictionCount: 3,
		LeadingHypothesis: &epistemic.ObjectSummary{Label: "H1", Summary: "H1: prompt lost before runtime"},
		ActiveFrame:       &epistemic.ObjectSummary{Label: "F1", Summary: "F1: propagate instructions"},
		LastTransition: &epistemic.TransitionSummary{
			From: epistemic.PhaseExecute, To: epistemic.PhaseAbduce,
			Reason: strings.Repeat("runtime capture contradicted prior state ", 8),
		},
	}
	clean := ansi.Strip(newModel().SetSize(80, 24).SetOperatorView(&view, "SHADE").SetOperatorSupplement(epistemic.OperatorSupplement{
		TerminalReason: "reopen budget exhausted", InvalidatedPhases: []epistemic.Phase{epistemic.PhaseFrame},
	}).View())
	for _, want := range []string{"INCOMPLETE", "ABDUCE", "H1", "F1", "!F", "UNKNOWN", "CONTRA", "reopen budget exhau"} {
		if !strings.Contains(strings.ToUpper(clean), strings.ToUpper(want)) {
			t.Fatalf("small incomplete view missing %q:\n%s", want, clean)
		}
	}
}

func TestCESViewDoesNotReuseAStaleQACResource(t *testing.T) {
	view := epistemic.OperatorView{WorkStatus: epistemic.WorkActive, Phase: epistemic.PhaseTriage}
	m := newModel().SetQAC(true, "stale-resource").SetOperatorView(&view, "")
	_, _, cognition, _ := m.presentationStatus()
	if cognition != "—" {
		t.Fatalf("cognition = %q, want no selected resource", cognition)
	}
}

func TestQACResourceUpdatesCognitionWithoutChangingCESWork(t *testing.T) {
	view := epistemic.OperatorView{WorkStatus: epistemic.WorkActive, Phase: epistemic.PhaseExecute}
	m := newModel().SetOperatorView(&view, "old-resource").SetQAC(true, "new-resource")
	work, phase, cognition, _ := m.presentationStatus()
	if work != "ACTIVE" || phase != "EXECUTE" || cognition != "NEW-RESOURCE" {
		t.Fatalf("presentation = work %q, phase %q, cognition %q", work, phase, cognition)
	}
}

func TestInspectorShortcutOpensEpistemicState(t *testing.T) {
	_, cmd := newModel().Update(press('e', "e"))
	if cmd == nil {
		t.Fatal("E did not open an epistemic state")
	}
	if _, ok := cmd().(OpenEpistemicMsg); !ok {
		t.Fatalf("E message = %T, want OpenEpistemicMsg", cmd())
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
