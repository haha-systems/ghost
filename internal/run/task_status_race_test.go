package run

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/runtime"
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

// TestDelegatedChildResultBeforeOwnerSyncIsAccepted pins the ordering that
// stranded delegated children under load: the router creates the child task
// and delivers it, and the child replies before the owner has synced the
// router snapshot that contains the task. The result must still be accepted.
func TestDelegatedChildResultBeforeOwnerSyncIsAccepted(t *testing.T) {
	c, sessions := autoConsultController(t, false, nil)
	veilReplied := make(chan struct{})
	var hold sync.Once
	if err := c.call(func(o *owner) {
		router := o.router
		o.afterBrokerApply = func() {
			// Hold the owner sync after the command that created the child
			// until VEIL's result has been observed.
			if len(router.Snapshot().Tasks) < 2 {
				return
			}
			hold.Do(func() {
				select {
				case <-veilReplied:
				case <-time.After(5 * time.Second):
				}
			})
		}
	}); err != nil {
		t.Fatal(err)
	}
	watchBrokerSessions(t, c, sessions)
	// WRAITH's first turn is driven by the test below; later turns, such as
	// receiving the child's result, close normally.
	var wraithSends atomic.Int32
	sessions["wraith"].Session.OnSend = func(runtime.Input) []runtime.Event {
		if wraithSends.Add(1) == 1 {
			return nil
		}
		return channelEvent("wraith", "The delegated review failed; closing the task.")
	}
	var projectedBeforeReply atomic.Bool
	var replies atomic.Int32
	sessions["veil"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		child := frameValue(input.Text, "task_id")
		if child == "" || replies.Add(1) != 1 {
			return nil
		}
		_ = c.call(func(o *owner) {
			_, ok := o.workspace.task(core.TaskID(child))
			projectedBeforeReply.Store(ok)
		})
		block := "```ghost-result\n" + `{"schema_version":1,"task_id":"` + child + `","status":"failed","result":"The review could not finish"}` + "\n```"
		for _, item := range channelEvent("veil", block) {
			_ = c.Observe(item)
		}
		close(veilReplied)
		return nil
	}
	if err := c.Send([]string{"wraith"}, "Review a small area"); err != nil {
		t.Fatal(err)
	}
	// Let WRAITH's delivery finish first: a finishing delivery also syncs the
	// projection, and VEIL's must be the only delivery in flight while the
	// broker holds the sync.
	var parent core.TaskID
	waitFor(t, func() bool {
		tasks := c.WorkspaceSnapshot().Tasks
		if len(tasks) != 1 {
			return false
		}
		parent = tasks[0].ID
		pending := 1
		_ = c.call(func(o *owner) { pending = o.pendingChannelWork[parent] })
		return pending == 0 && len(sessions["wraith"].capturedInputs()) == 1
	})
	block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + string(parent) + `","objective":"Review a small area","context_refs":[],"expected_deliverable":"One finding"}` + "\n```"
	// Emit WRAITH's reply through its session, as the fake would have from
	// Send, so its turn completes and later deliveries use Send again.
	wraith := sessions["wraith"].Session
	for _, item := range channelEvent("wraith", block) {
		item.Time = time.Now()
		if item.Metadata["backend_method"] == "turn/completed" {
			wraith.Guard.Complete(item.TurnID, false)
		}
		wraith.Emit(item)
	}
	waitFor(t, func() bool {
		w := c.WorkspaceSnapshot()
		return len(w.Tasks) == 2 && w.Tasks[0].Status.IsTerminal() && w.Tasks[1].Status.IsTerminal()
	})
	if projectedBeforeReply.Load() {
		t.Fatal("child task was already projected when VEIL replied; the test did not pin the race")
	}
	w := c.WorkspaceSnapshot()
	if w.Tasks[1].Status != core.TaskFailed || w.Tasks[0].Status != core.TaskCompleted {
		t.Fatalf("task statuses = parent %s, child %s", w.Tasks[0].Status, w.Tasks[1].Status)
	}
	for _, e := range c.Events(EventFilter{}) {
		if strings.Contains(string(e.Payload), "does not own an open delegated task") {
			t.Fatalf("valid child result was rejected: %s", e.Payload)
		}
	}
	if err := c.Status().CoreError; err != nil {
		t.Fatalf("core error = %v", err)
	}
}

// TestChildResultForTaskItDoesNotOwnIsRejected keeps the error path for
// genuinely invalid results now that ownership is checked in the broker.
func TestChildResultForTaskItDoesNotOwnIsRejected(t *testing.T) {
	c, sessions := autoConsultController(t, false, nil)
	watchBrokerSessions(t, c, sessions)
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		parent := frameValue(input.Text, "task_id")
		if parent == "" {
			return nil
		}
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + parent + `","objective":"Review a small area","context_refs":[],"expected_deliverable":"One finding"}` + "\n```"
		return channelEvent("wraith", block)
	}
	var child atomic.Value
	sessions["veil"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		if id := frameValue(input.Text, "task_id"); id != "" {
			child.CompareAndSwap(nil, id)
		}
		return nil
	}
	if err := c.Send([]string{"wraith"}, "Review a small area"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return child.Load() != nil && len(c.WorkspaceSnapshot().Tasks) == 2 })
	id := child.Load().(string)
	// SHADE does not own the child, and nobody owns an unknown task.
	for _, taskID := range []string{id, "task_unknown"} {
		block := "```ghost-result\n" + `{"schema_version":1,"task_id":"` + taskID + `","status":"completed","result":"not mine"}` + "\n```"
		for _, item := range channelEvent("shade", block) {
			if err := c.Observe(item); err != nil {
				t.Fatal(err)
			}
		}
	}
	waitFor(t, func() bool {
		rejected := 0
		for _, e := range c.Events(EventFilter{}) {
			if e.Type == "coordination.routing_rejected" && strings.Contains(string(e.Payload), "does not own an open delegated task") {
				rejected++
			}
		}
		return rejected == 2
	})
	if got := c.WorkspaceSnapshot().Tasks[1].Status; got.IsTerminal() {
		t.Fatalf("child status = %s after invalid results", got)
	}
}
