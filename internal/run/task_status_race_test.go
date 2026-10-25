package run

import (
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/core"
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
	if got := keepNewerTaskStatus(queued, running, time.Time{}, time.Time{}); got.Status != "running" || got.StartedAt == nil {
		t.Fatalf("stale router snapshot regressed status: %+v", got)
	}
	completed := running
	if err := completed.Transition("completed", running.UpdatedAt.Add(1)); err != nil {
		t.Fatal(err)
	}
	if got := keepNewerTaskStatus(completed, running, time.Time{}, time.Time{}); got.Status != "completed" {
		t.Fatalf("newer router status was not taken: %+v", got)
	}
}

func TestReplaceBrokerRecordsKeepsNewerProjectedTaskOwner(t *testing.T) {
	store := testWorkspace(t)
	task := testWorkspaceTask(t, store, "wraith")
	if err := store.addTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.transitionTask(task.ID, "running", task.CreatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	staleRouterTask, _ := store.task(task.ID)
	if err := store.assignTask(task.ID, "shade"); err != nil {
		t.Fatal(err)
	}

	store.replaceBrokerRecords([]core.Task{staleRouterTask}, nil, nil)
	projected, ok := store.task(task.ID)
	shade, _ := store.agentID("shade")
	if !ok || projected.Owner != shade || !containsAgent(projected.Participants, shade) {
		t.Fatalf("stale broker snapshot replaced the projected owner: %#v", projected)
	}
	for _, agent := range store.snapshot().Agents {
		if agent.ID != shade {
			continue
		}
		for _, active := range agent.ActiveTask {
			if active == task.ID {
				return
			}
		}
	}
	t.Fatal("new owner did not retain the active task after broker sync")
}
