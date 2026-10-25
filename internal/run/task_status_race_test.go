package run

import (
	"strings"
	"testing"
)

// TestFailedDelegatedChildDoesNotRegressParentStatus is a regression test for a
// race found while fixing CI: a broker snapshot taken before a queued parent
// transition reached the router overwrote the projected parent status with the
// stale "queued" one, so completing the parent failed with
// `invalid task transition "queued" -> "completed"` (about 1-5% of runs).
func TestFailedDelegatedChildDoesNotRegressParentStatus(t *testing.T) {
	for range 25 {
		c, sessions := autoConsultController(t, false, nil)
		startFailedDelegatedChild(t, c, sessions)
		waitFor(t, func() bool {
			w := c.WorkspaceSnapshot()
			return len(w.Tasks) == 2 && w.Tasks[0].Status.IsTerminal() && w.Tasks[1].Status.IsTerminal()
		})
		if err := c.Status().CoreError; err != nil {
			t.Fatalf("core error = %v", err)
		}
		completions := 0
		for _, e := range c.Events(EventFilter{Type: "task.status_changed"}) {
			if e.Source == "controller" && strings.Contains(string(e.Payload), `"to":"completed"`) {
				completions++
			}
		}
		if completions != 1 {
			t.Fatalf("parent completed %d times, want 1", completions)
		}
		_ = c.Close()
	}
}

func TestKeepNewerTaskStatusIgnoresStaleRouterSnapshot(t *testing.T) {
	c, _ := autoConsultController(t, false, nil)
	if err := c.Send([]string{"wraith"}, "probe"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(c.WorkspaceSnapshot().Tasks) == 1 })
	queued := c.WorkspaceSnapshot().Tasks[0]
	queued.Status = "queued"
	queued.StartedAt, queued.FinishedAt = nil, nil
	running := queued
	if err := running.Transition("running", queued.UpdatedAt.Add(1)); err != nil {
		t.Fatal(err)
	}
	if got := keepNewerTaskStatus(queued, running); got.Status != "running" || got.StartedAt == nil {
		t.Fatalf("stale router snapshot regressed status: %+v", got)
	}
	completed := running
	if err := completed.Transition("completed", running.UpdatedAt.Add(1)); err != nil {
		t.Fatal(err)
	}
	if got := keepNewerTaskStatus(completed, running); got.Status != "completed" {
		t.Fatalf("newer router status was not taken: %+v", got)
	}
}
