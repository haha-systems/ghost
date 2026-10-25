package run

import (
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
)

func TestControllerWorkspaceProjectsCompletedCESRun(t *testing.T) {
	c, _, events := newScript(t, scriptsFor(chainArtifacts)...)
	initial := c.WorkspaceSnapshot()
	if len(initial.Agents) != 2 || len(c.TasksForAgent("wraith")) != 0 {
		t.Fatalf("initial workspace = %#v", initial)
	}
	for _, agent := range initial.Agents {
		if agent.Availability != core.AvailabilityAvailable {
			t.Fatalf("installed session is not available: %#v", agent)
		}
	}

	if err := c.Start("make the sequence visible"); err != nil {
		t.Fatal(err)
	}
	status, _ := terminalEvents(t, events)
	if status.State.Task.Status != epistemic.WorkComplete {
		t.Fatalf("CES work status = %s", status.State.Task.Status)
	}
	if c.Status().Requested != CESPolicyOn || c.Status().Effective != CESPolicyOn || len(c.Events(EventFilter{Type: "ces.phase"})) == 0 {
		t.Fatal("configured CES policy did not run CES phases")
	}

	workspace := c.WorkspaceSnapshot()
	if len(workspace.Tasks) != 1 || workspace.Tasks[0].Requester != core.HumanAgentID || workspace.Tasks[0].Status != core.TaskCompleted {
		t.Fatalf("projected task = %#v", workspace.Tasks)
	}
	if len(workspace.Messages) != 0 {
		t.Fatalf("CES phase telemetry must not become broker messages: %#v", workspace.Messages)
	}
	if got := c.Events(EventFilter{Type: "decision.qac"}); len(got) == 0 {
		t.Fatal("QAC decisions are missing from the workspace event view")
	}
	if got := c.Events(EventFilter{Category: core.CategoryTask, Type: "task.status_changed"}); len(got) < 3 {
		t.Fatalf("task lifecycle events = %d", len(got))
	}
	for _, item := range workspace.Events {
		if err := item.Validate(); err != nil {
			t.Fatalf("invalid projected event %s: %v", item.Type, err)
		}
	}
}

func TestStandardTaskLifecycleAndRepliesUseRuntimeEvents(t *testing.T) {
	c, _, _ := newScript(t, []string{})
	defer c.Close()
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("ask both agents"); err != nil {
		t.Fatal(err)
	}
	initial := c.WorkspaceSnapshot()
	if len(initial.Tasks) != 1 || initial.Tasks[0].Status != core.TaskQueued || len(initial.Tasks[0].Participants) != 2 {
		t.Fatalf("queued standard task = %#v", initial.Tasks)
	}
	emit := func(agent, method, kind, summary string) {
		t.Helper()
		if err := c.Observe(runtime.Event{
			Time: time.Now(), AgentID: agent, SessionID: agent, TurnID: "turn-" + agent,
			Kind: runtime.EventKind(kind), Summary: summary,
			Metadata: map[string]string{"backend_method": method},
		}); err != nil {
			t.Fatal(err)
		}
	}
	emit("shade", "turn/started", string(runtime.KindStatus), "")
	emit("shade", "item/agentMessage/completed", string(runtime.KindMessage), "shade reply")
	emit("shade", "turn/completed", string(runtime.KindStatus), "")
	if got := c.WorkspaceSnapshot().Tasks[0].Status; got != core.TaskRunning {
		t.Fatalf("task completed before all participants: %s", got)
	}
	emit("wraith", "turn/started", string(runtime.KindStatus), "")
	emit("wraith", "item/agentMessage/completed", string(runtime.KindMessage), "wraith reply")
	emit("wraith", "turn/completed", string(runtime.KindStatus), "")
	workspace := c.WorkspaceSnapshot()
	if workspace.Tasks[0].Status != core.TaskCompleted || len(workspace.Messages) != 3 {
		t.Fatalf("completed standard workspace = %#v", workspace)
	}
	for _, message := range workspace.Messages {
		if message.TaskID == nil || *message.TaskID != workspace.Tasks[0].ID || message.ThreadID != workspace.Tasks[0].ThreadID {
			t.Fatalf("message scope = %#v", message)
		}
	}
	if len(c.Events(EventFilter{Type: "ces.phase"})) != 0 {
		t.Fatal("CES phase event appeared in the standard path")
	}
}

func TestControllerSendRoutesOnlyAddressedAgentsDuringActiveRun(t *testing.T) {
	c, _, events := newScript(t, []string{})
	if err := c.Start("keep the CES phase open"); err != nil {
		t.Fatal(err)
	}
	if err := c.Send([]string{"shade"}, "please review this"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		states := map[string]bool{}
		_ = c.call(func(o *owner) {
			for name, session := range o.sessions {
				states[name] = session.State() == runtime.StateRunning
			}
		})
		return states["shade"] && !states["wraith"]
	})
	messages := c.AllMessages()
	last := messages[len(messages)-1]
	if len(last.Recipients) != 1 {
		t.Fatalf("message recipients = %v", last.Recipients)
	}
	var shadeID core.AgentID
	for _, agent := range c.WorkspaceSnapshot().Agents {
		if agent.DisplayName == "shade" {
			shadeID = agent.ID
		}
	}
	if last.Recipients[0] != shadeID {
		t.Fatalf("message recipient = %q, want shade %q", last.Recipients[0], shadeID)
	}
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	_, _ = terminalEvents(t, events)
}

func TestControllerSendBroadcastRoutesAllAgents(t *testing.T) {
	c, _, events := newScript(t, []string{})
	if err := c.Start("keep the CES phase open"); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(nil, "@all please report"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		states := map[string]bool{}
		_ = c.call(func(o *owner) {
			for name, session := range o.sessions {
				states[name] = session.State() == runtime.StateRunning
			}
		})
		return states["shade"] && states["wraith"]
	})
	messages := c.AllMessages()
	last := messages[len(messages)-1]
	if last.Type != core.MessageBroadcast || len(last.Recipients) != 2 {
		t.Fatalf("broadcast message = %#v", last)
	}
	for _, recipient := range last.Recipients {
		if last.RecipientDelivery[recipient] != core.DeliveryDelivered {
			t.Fatalf("broadcast recipient %q state = %s", recipient, last.RecipientDelivery[recipient])
		}
	}
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	_, _ = terminalEvents(t, events)
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}
