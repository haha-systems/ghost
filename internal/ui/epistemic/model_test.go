package epistemic

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	ces "github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

func keyPress(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

func inspectorFixture() Snapshot {
	return Snapshot{
		Operator: ces.OperatorView{WorkStatus: ces.WorkActive, Phase: ces.PhaseAbduce},
		Objects: []Object{
			{ID: "opaque-h1", Alias: "H1", Kind: ces.ObjectKindHypothesis, Label: "prompt lost before runtime", Summary: "Prompt content is lost before the runtime boundary.", Status: "leading", ProducedBy: "abducer / SHADE", Revision: "r4", Falsifier: "capture configured prompt entering runtime"},
			{ID: "opaque-u1", Alias: "U1", Kind: ces.ObjectKindUnknown, Label: "instruction channel", Summary: "Which field carries the prompt?", Status: "open"},
			{ID: "opaque-f1", Alias: "F1", Kind: ces.ObjectKindFrame, Label: "propagate instructions", Summary: "Carry instructions across the runtime boundary.", Status: "active"},
			{ID: "opaque-o8", Alias: "O8", Kind: ces.ObjectKindObservation, Label: "session config", Summary: "SessionConfig lacked instruction representation."},
			{ID: "opaque-c2", Alias: "C2", Kind: ces.ObjectKindClaim, Label: "provider mapping", Summary: "The provider maps developer instructions."},
		},
		Relations: []Relation{
			{ID: "opaque-r1", Alias: "R1", Kind: "supports", SourceAlias: "O8", TargetAlias: "H1", Status: "active", SourceSummary: "SessionConfig lacked instruction representation.", ProducedBy: "abducer / SHADE"},
			{ID: "opaque-r2", Alias: "R2", Kind: "contradicts", SourceAlias: "C2", TargetAlias: "H2", Status: "retracted", SourceSummary: "The provider maps developer instructions.", RetractionReason: "new capture invalidated the relation"},
		},
	}
}

func TestInspectorOverviewShowsHypothesesUnknownsFramesAndRelations(t *testing.T) {
	m := New(theme.Bloodwire(), keymap.Default()).SetSnapshot(inspectorFixture()).SetSize(120, 40)
	out := m.View()
	for _, want := range []string{"EPISTEMIC STATE", "HYPOTHESES", "H1", "LEADING", "UNKNOWNS", "U1", "OPEN", "FRAME", "F1", "CONTRADICTIONS", "R2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspector overview missing %q", want)
		}
	}
}

func TestInspectorShowsSemanticCognitionTrajectory(t *testing.T) {
	m := New(theme.Bloodwire(), keymap.Default()).SetSnapshot(inspectorFixture()).SetSize(120, 40)
	for _, item := range []event.Event{
		{Time: time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC), Source: "CES", Kind: event.KindPhase, Metadata: map[string]string{"from": "triage", "to": "abduce"}},
		{Kind: event.KindHypothesis, Metadata: map[string]string{"status": "leading", "alias": "H1", "summary": "prompt lost before runtime"}},
		{Kind: event.KindReopen, Metadata: map[string]string{"from": "execute", "to": "abduce", "reason": "runtime capture contradicts the leading hypothesis", "reopen_count": "1", "reopen_limit": "3"}},
	} {
		m = m.AppendEvent(item)
	}
	out := strings.ToLower(m.View())
	for _, want := range []string{"cognitive trajectory", "09:00:00 ces", "triage -> abduce", "h1", "execute -> abduce", "runtime capture contradicts the leading hypothesis"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspector trajectory missing %q", want)
		}
	}
}

func TestHypothesisDetailShowsProvenanceAndCounterevidence(t *testing.T) {
	m := New(theme.Bloodwire(), keymap.Default()).SetSnapshot(inspectorFixture()).SetSize(120, 40)
	m, _ = m.Update(keyPress(tea.KeyEnter, "enter"))
	if !m.DetailOpen() {
		t.Fatal("Enter did not open the selected hypothesis")
	}
	out := m.View()
	for _, want := range []string{"HYPOTHESIS", "LEADING", "ABDUCER / SHADE", "FALSIFIER", "capture configured prompt", "SUPPORT", "O8", "COUNTEREVIDENCE", "none"} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Fatalf("hypothesis detail missing %q", want)
		}
	}
}

func TestInspectorShowsRetractedRelationAndReason(t *testing.T) {
	snapshot := Snapshot{Relations: inspectorFixture().Relations}
	m := New(theme.Bloodwire(), keymap.Default()).SetSnapshot(snapshot).SetSize(120, 40)
	m, _ = m.Update(keyPress(tea.KeyEnter, "enter"))
	out := strings.ToLower(m.View())
	for _, want := range []string{"retracted", "contradicts", "provider maps developer instructions", "new capture invalidated the relation"} {
		if !strings.Contains(out, want) {
			t.Fatalf("relation detail missing %q", want)
		}
	}
}

func TestRelationOverviewUsesCompactIDsWhenAliasesAreMissing(t *testing.T) {
	snapshot := Snapshot{Relations: []Relation{{
		ID: "relation-001", Kind: "supports", SourceID: "observation-123456", TargetID: "hypothesis-987654", Status: "active",
	}}}
	m := New(theme.Bloodwire(), keymap.Default()).SetSnapshot(snapshot).SetSize(120, 40)
	out := m.View()
	for _, want := range []string{"…123456", "…987654"} {
		if !strings.Contains(out, want) {
			t.Fatalf("relation overview missing compact ID %q:\n%s", want, out)
		}
	}
}

func TestInspectorCanOpenObservationAndClaimDetails(t *testing.T) {
	snapshot := Snapshot{Objects: inspectorFixture().Objects[3:]}
	m := New(theme.Bloodwire(), keymap.Default()).SetSnapshot(snapshot).SetSize(120, 40)
	for i := 0; i < 1; i++ {
		m, _ = m.Update(keyPress(tea.KeyDown, "down"))
	}
	m, _ = m.Update(keyPress(tea.KeyEnter, "enter"))
	if !strings.Contains(strings.ToLower(m.View()), "claim") {
		t.Fatal("Claim detail was not available from the overview")
	}
	m, cmd := m.Update(keyPress(tea.KeyEscape, "esc"))
	if cmd != nil {
		t.Fatal("Escape from detail should return to the overview")
	}
	m, _ = m.Update(keyPress(tea.KeyDown, "down"))
	m, _ = m.Update(keyPress(tea.KeyEnter, "enter"))
	if !strings.Contains(strings.ToLower(m.View()), "observation") {
		t.Fatalf("Observation detail was not available from the overview:\n%s", m.View())
	}
}

func TestInspectorSupportsScrollingAndResizing(t *testing.T) {
	snapshot := Snapshot{}
	for i := 0; i < 40; i++ {
		snapshot.Objects = append(snapshot.Objects, Object{ID: ces.ID(fmt.Sprintf("opaque-%d", i)), Alias: fmt.Sprintf("O%d", i), Kind: ces.ObjectKindObservation, Summary: strings.Repeat("evidence ", 20)})
	}
	m := New(theme.Bloodwire(), keymap.Default()).SetSnapshot(snapshot).SetSize(80, 24)
	if lipgloss.Height(m.View()) != 24 || lipgloss.Width(m.View()) != 80 {
		t.Fatal("inspector did not fit the minimum supported terminal")
	}
	m, _ = m.Update(keyPress(tea.KeyPgUp, "pgup"))
	if m.content.Follow() {
		t.Fatal("Page Up did not scroll the inspector")
	}
	m = m.SetSize(120, 40)
	if lipgloss.Height(m.View()) != 40 || lipgloss.Width(m.View()) != 120 {
		t.Fatal("inspector did not resize to the new terminal")
	}
	if !strings.Contains(m.View(), "EPISTEMIC STATE") {
		t.Fatal("resize lost the inspector content")
	}
}

func TestEscapeReturnsFromOverviewToDashboard(t *testing.T) {
	m := New(theme.Bloodwire(), keymap.Default()).SetSnapshot(inspectorFixture()).SetSize(100, 30)
	_, cmd := m.Update(keyPress(tea.KeyEscape, "esc"))
	if cmd == nil {
		t.Fatal("Escape from overview did not return to the dashboard")
	}
	if _, ok := cmd().(BackMsg); !ok {
		t.Fatalf("Escape message = %T, want BackMsg", cmd())
	}
}
