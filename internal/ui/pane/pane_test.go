package pane

import (
	"fmt"
	"strings"
	"testing"
)

func filled(lines int) string {
	rows := make([]string, lines)
	for i := range rows {
		rows[i] = fmt.Sprintf("line %d", i)
	}
	return strings.Join(rows, "\n")
}

// sized returns a pane showing 5 rows of a 50-row log, at the bottom.
func sized(t *testing.T) Pane {
	t.Helper()
	p := New()
	p.SetSize(20, 5)
	p.SetContent(filled(50))
	return p
}

func TestPaneStartsFollowing(t *testing.T) {
	p := sized(t)
	if !p.Follow() {
		t.Fatal("a new pane is not following")
	}
	if p.NewCount() != 0 {
		t.Fatalf("new count = %d, want 0", p.NewCount())
	}
}

func TestScrollingUpSuspendsFollow(t *testing.T) {
	p := sized(t)
	p.ScrollUp(1)
	if p.Follow() {
		t.Fatal("scrolling up left the pane following")
	}
}

func TestArrivalsWhileFollowingDoNotAccumulate(t *testing.T) {
	p := sized(t)
	p.Noted(3)
	if p.NewCount() != 0 {
		t.Fatalf("following pane counted %d new entries", p.NewCount())
	}
}

func TestArrivalsWhileDetachedAreCounted(t *testing.T) {
	p := sized(t)
	p.ScrollUp(2)
	p.Noted(4)
	p.Noted(3)
	if p.NewCount() != 7 {
		t.Fatalf("new count = %d, want 7", p.NewCount())
	}
}

// The operator must never be yanked back to the bottom by arriving output.
func TestDetachedPaneDoesNotMoveWhenContentGrows(t *testing.T) {
	p := sized(t)
	p.ScrollUp(3)
	before := p.View()
	p.SetContent(filled(80))
	p.Noted(30)
	if p.View() != before {
		t.Fatal("arriving content moved a detached pane")
	}
}

func TestReturningToBottomResumesFollowAndClearsCount(t *testing.T) {
	p := sized(t)
	p.ScrollUp(3)
	p.Noted(5)
	p.GotoBottom()
	if !p.Follow() {
		t.Fatal("reaching the bottom did not resume follow")
	}
	if p.NewCount() != 0 {
		t.Fatalf("new count = %d, want 0 after resuming", p.NewCount())
	}
}

// Scrolling back down by hand resumes follow just as End does.
func TestScrollingBackToBottomResumesFollow(t *testing.T) {
	p := sized(t)
	p.ScrollUp(2)
	p.ScrollDown(2)
	if !p.Follow() {
		t.Fatal("scrolling back to the bottom did not resume follow")
	}
}

func TestGotoTopSuspendsFollow(t *testing.T) {
	p := sized(t)
	p.GotoTop()
	if p.Follow() {
		t.Fatal("jumping to the top left the pane following")
	}
}

func TestPageNavigationMoves(t *testing.T) {
	p := sized(t)
	p.PageUp()
	if p.Follow() {
		t.Fatal("page up left the pane following")
	}
	p.PageDown()
	p.PageDown()
	if !p.Follow() {
		t.Fatal("paging back down did not reach the bottom")
	}
}

// A following pane tracks growth so the newest output stays visible.
func TestFollowingPaneStaysAtTheBottom(t *testing.T) {
	p := sized(t)
	p.SetContent(filled(80))
	if !strings.Contains(p.View(), "line 79") {
		t.Fatal("following pane did not stay at the newest content")
	}
}

func TestResetReturnsToLiveOutput(t *testing.T) {
	p := sized(t)
	p.ScrollUp(3)
	p.Noted(9)
	p.Reset()
	if !p.Follow() || p.NewCount() != 0 {
		t.Fatalf("reset left follow=%v count=%d", p.Follow(), p.NewCount())
	}
}
