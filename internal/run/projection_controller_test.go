package run

import (
	"strings"
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
	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Tasks) == 1 && workspace.Tasks[0].Status == core.TaskCompleted && len(workspace.Messages) == 3
	})
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

func TestCodexAgentMessageLifecycleProjectsOnlyCompletedReply(t *testing.T) {
	c, _, _ := newScript(t, []string{})
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("ask both agents"); err != nil {
		t.Fatal(err)
	}
	if got := c.WorkspaceSnapshot().Tasks; len(got) != 1 || len(got[0].Participants) != 2 {
		t.Fatalf("started tasks=%#v", got)
	}
	emit := func(agent, session, turn, method string, kind runtime.EventKind, summary string) {
		t.Helper()
		if err := c.Observe(runtime.Event{
			Time: time.Now(), AgentID: agent, SessionID: session, TurnID: turn,
			Kind: kind, Summary: summary,
			Metadata: map[string]string{"backend_method": method},
		}); err != nil {
			t.Fatal(err)
		}
	}
	const turnID = "codex-turn-1"
	emit("shade", "thread-shade", turnID, "turn/started", runtime.KindStatus, "turn/started")
	emit("shade", "thread-shade", turnID, "item/started", runtime.KindStatus, "item/started")
	emit("shade", "thread-shade", turnID, "item/agentMessage/delta", runtime.KindMessage, "Hel")
	emit("shade", "thread-shade", turnID, "item/agentMessage/delta", runtime.KindMessage, "lo")
	emit("shade", "thread-shade", turnID, "item/agentMessage/delta", runtime.KindMessage, " world")
	var buffered string
	_ = c.call(func(o *owner) { buffered = o.replyBuffers["thread-shade/"+turnID] })
	if buffered != "Hello world" {
		t.Fatalf("buffered reply=%q, want %q", buffered, "Hello world")
	}
	emit("shade", "thread-shade", turnID, "item/completed", runtime.KindMessage, "Hello world")
	emit("shade", "thread-shade", turnID, "turn/completed", runtime.KindStatus, "turn completed")
	// The other participant completes its turn without an assistant message.
	emit("wraith", "thread-wraith", "codex-turn-2", "turn/started", runtime.KindStatus, "turn/started")
	emit("wraith", "thread-wraith", "codex-turn-2", "turn/completed", runtime.KindStatus, "turn completed")

	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		if len(workspace.Tasks) != 1 || workspace.Tasks[0].Status != core.TaskCompleted {
			return false
		}
		pending := -1
		_ = c.call(func(o *owner) { pending = o.pendingChannelWork[workspace.Tasks[0].ID] })
		return pending == 0
	})
	workspace := c.WorkspaceSnapshot()
	if workspace.Tasks[0].Status != core.TaskCompleted {
		t.Fatalf("task status=%s, want completed", workspace.Tasks[0].Status)
	}
	replies := 0
	for _, message := range workspace.Messages {
		if strings.Contains(message.Content, "item/started") {
			t.Fatalf("lifecycle notification became a message: %#v", message)
		}
		if message.Sender != core.HumanAgentID && message.Type == core.MessageReply {
			replies++
			if message.Content != "Hello world" {
				t.Fatalf("agent reply=%q, want full completed text", message.Content)
			}
		}
	}
	if replies != 1 {
		t.Fatalf("recorded agent replies=%d, want 1; messages=%#v", replies, workspace.Messages)
	}
	var remaining string
	_ = c.call(func(o *owner) { remaining = o.replyBuffers["thread-shade/"+turnID] })
	if remaining != "" {
		t.Fatalf("completed reply buffer=%q, want cleared", remaining)
	}
}

func TestProjectionDoesNotTreatNonFinalMessageEventAsAgentReply(t *testing.T) {
	c, _, _ := newScript(t, []string{})
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("ask both agents"); err != nil {
		t.Fatal(err)
	}
	emit := func(agent, turn, method string, kind runtime.EventKind, summary string) {
		t.Helper()
		if err := c.Observe(runtime.Event{
			Time: time.Now(), AgentID: agent, SessionID: "thread-" + agent, TurnID: turn,
			Kind: kind, Summary: summary,
			Metadata: map[string]string{"backend_method": method},
		}); err != nil {
			t.Fatal(err)
		}
	}
	emit("shade", "turn-1", "turn/started", runtime.KindStatus, "turn/started")
	// This guards the projection even if a provider adapter mislabels a lifecycle event.
	emit("shade", "turn-1", "item/started", runtime.KindMessage, "item/started")
	emit("shade", "turn-1", "turn/completed", runtime.KindStatus, "turn completed")
	emit("wraith", "turn-2", "turn/started", runtime.KindStatus, "turn/started")
	emit("wraith", "turn-2", "turn/completed", runtime.KindStatus, "turn completed")
	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		if len(workspace.Tasks) != 1 || workspace.Tasks[0].Status != core.TaskCompleted {
			return false
		}
		pending := -1
		_ = c.call(func(o *owner) { pending = o.pendingChannelWork[workspace.Tasks[0].ID] })
		return pending == 0
	})
	for _, message := range c.WorkspaceSnapshot().Messages {
		if message.Sender != core.HumanAgentID && message.Type == core.MessageReply {
			t.Fatalf("non-final event became an agent reply: %#v", message)
		}
	}
}

func TestCodexCompletedMessageFallsBackToBufferedDeltas(t *testing.T) {
	c, _, _ := newScript(t, []string{})
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("ask both agents"); err != nil {
		t.Fatal(err)
	}
	emit := func(agent, session, turn, method string, kind runtime.EventKind, summary string) {
		t.Helper()
		if err := c.Observe(runtime.Event{
			Time: time.Now(), AgentID: agent, SessionID: session, TurnID: turn,
			Kind: kind, Summary: summary,
			Metadata: map[string]string{"backend_method": method},
		}); err != nil {
			t.Fatal(err)
		}
	}
	emit("shade", "thread-shade", "turn-1", "turn/started", runtime.KindStatus, "turn/started")
	emit("shade", "thread-shade", "turn-1", "item/agentMessage/delta", runtime.KindMessage, "Hello ")
	emit("shade", "thread-shade", "turn-1", "item/agentMessage/delta", runtime.KindMessage, "world")
	emit("shade", "thread-shade", "turn-1", "item/completed", runtime.KindMessage, "")
	emit("shade", "thread-shade", "turn-1", "turn/completed", runtime.KindStatus, "turn completed")
	emit("wraith", "thread-wraith", "turn-2", "turn/started", runtime.KindStatus, "turn/started")
	emit("wraith", "thread-wraith", "turn-2", "turn/completed", runtime.KindStatus, "turn completed")
	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		if len(workspace.Tasks) != 1 || workspace.Tasks[0].Status != core.TaskCompleted {
			return false
		}
		pending := -1
		_ = c.call(func(o *owner) { pending = o.pendingChannelWork[workspace.Tasks[0].ID] })
		return pending == 0
	})
	replies := 0
	for _, message := range c.WorkspaceSnapshot().Messages {
		if message.Sender != core.HumanAgentID && message.Type == core.MessageReply {
			replies++
			if message.Content != "Hello world" {
				t.Fatalf("fallback reply=%q, want %q", message.Content, "Hello world")
			}
		}
	}
	if replies != 1 {
		t.Fatalf("agent replies=%d, want 1", replies)
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
