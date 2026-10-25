package run

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/memory"
	"github.com/haha-systems/ghost/internal/memory/memorytest"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
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

func TestCESOwnerChangeSurvivesBrokerSyncAndReplay(t *testing.T) {
	cfg := appQACConfig()
	backend := memorytest.New()
	factory := func(workspace core.WorkspaceID, agents map[string]core.AgentID, sink func(core.Event)) MemoryService {
		authorized := make([]core.AgentID, 0, len(agents))
		for _, agent := range agents {
			authorized = append(authorized, agent)
		}
		return memory.NewService(backend, memory.Config{
			Enabled: true, Workspace: workspace, WorkspaceSpace: "workspace",
			AuthorizedAgents: authorized,
		}, memory.EventSink(sink))
	}
	firstGate, secondGate := make(chan struct{}), make(chan struct{})
	rt := &scriptRuntime{
		Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts),
		started: make(chan *scriptSession, 10), startGates: []<-chan struct{}{firstGate, secondGate},
	}
	workdir := t.TempDir()
	c := New(cfg, nil, Options{Runtime: rt, WorkDir: workdir, MemoryFactory: factory})
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetSessions(map[string]runtime.Session{
		"wraith": fake.NewSession("wraith"), "shade": fake.NewSession("shade"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("hand off this CES task"); err != nil {
		t.Fatal(err)
	}
	startedPhase := func() *scriptSession {
		t.Helper()
		select {
		case session := <-rt.started:
			return session
		case <-time.After(5 * time.Second):
			t.Fatal("CES phase did not start")
			return nil
		}
	}
	if session := startedPhase(); session.Agent != "wraith" {
		t.Fatalf("initial CES resource = %q, want wraith", session.Agent)
	}
	if err := c.Override("shade", "exercise cross-resource handoff"); err != nil {
		t.Fatal(err)
	}
	close(firstGate)
	if session := startedPhase(); session.Agent != "shade" {
		t.Fatalf("later CES resource = %q, want shade", session.Agent)
	}

	workspace := c.WorkspaceSnapshot()
	if len(workspace.Tasks) != 1 {
		t.Fatalf("CES tasks = %d, want 1", len(workspace.Tasks))
	}
	task := workspace.Tasks[0]
	var shadeID core.AgentID
	for _, agent := range workspace.Agents {
		if agent.DisplayName == "shade" {
			shadeID = agent.ID
		}
	}
	if shadeID == "" {
		t.Fatal("shade agent is missing from the workspace")
	}
	routerTask := func(controller *Controller) (core.Task, bool) {
		t.Helper()
		var got core.Task
		var ok bool
		if err := controller.call(func(o *owner) {
			if o.router != nil {
				got, ok = o.router.Snapshot().Task(task.ID)
			}
		}); err != nil {
			t.Fatal(err)
		}
		return got, ok
	}
	waitFor(t, func() bool {
		got, ok := routerTask(c)
		return ok && got.Owner == shadeID && got.Status == core.TaskRunning
	})

	// Force the same broker snapshot boundary as a task transition between phases.
	var syncDone <-chan error
	if err := c.call(func(o *owner) {
		taskID := task.ID
		syncDone = o.enqueueBroker(brokerCommand{apply: func(router *comms.Router) error {
			return router.TransitionTask(taskID, core.TaskRunning, "")
		}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-syncDone; err != nil {
		t.Fatal(err)
	}

	workspace = c.WorkspaceSnapshot()
	if len(workspace.Tasks) != 1 || workspace.Tasks[0].Owner != shadeID || !containsAgent(workspace.Tasks[0].Participants, shadeID) {
		t.Fatalf("projected handoff after broker sync = %#v", workspace.Tasks)
	}
	active := false
	for _, agent := range workspace.Agents {
		if agent.ID == shadeID {
			for _, id := range agent.ActiveTask {
				active = active || id == task.ID
			}
		}
	}
	if !active {
		t.Fatalf("shade active tasks = %#v", workspace.Agents)
	}
	if got, ok := routerTask(c); !ok || got.Owner != shadeID || !containsAgent(got.Participants, shadeID) {
		t.Fatalf("router task after broker sync = %#v, exists=%t", got, ok)
	}

	if _, err := c.RecallFor("shade", "handoff attribution"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return c.WorkspaceSnapshot().MemoryByTask[task.ID].Counters.Retrievals > 0
	})
	memoryHistory := c.WorkspaceSnapshot().MemoryByAgent[shadeID]
	for _, item := range memoryHistory.Events {
		if item.Type == core.MemoryEventRetrievalStarted && (item.AgentID != shadeID || item.TaskID != task.ID) {
			t.Fatalf("memory retrieval attribution = agent %s task %s", item.AgentID, item.TaskID)
		}
	}
	if memoryHistory.Counters.Retrievals == 0 {
		t.Fatalf("shade memory history = %#v", memoryHistory)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	history, err := readWorkspaceEventHistory(workdir, RecoveryLimits{})
	if err != nil {
		t.Fatal(err)
	}
	ownerChanges := 0
	var replayOwner core.AgentID
	for _, item := range history.Events {
		if item.Type == "task.owner_changed" {
			ownerChanges++
			var payload struct {
				To core.AgentID `json:"to"`
			}
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			replayOwner = payload.To
		}
	}
	if ownerChanges != 1 || replayOwner != shadeID {
		t.Fatalf("owner changes in event log = %d, final owner = %s; want one change to %s", ownerChanges, replayOwner, shadeID)
	}
	restarted := New(cfg, nil, Options{WorkDir: workdir})
	t.Cleanup(func() { _ = restarted.Close() })
	if got := restarted.WorkspaceSnapshot().Tasks; len(got) != 1 || got[0].ID != task.ID || got[0].Owner != shadeID {
		t.Fatalf("replayed task = %#v", got)
	}
	if got, ok := routerTask(restarted); !ok || got.Owner != shadeID {
		t.Fatalf("replayed router task = %#v, exists=%t", got, ok)
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
	// Per-recipient delivery completes asynchronously from the session turn.
	waitFor(t, func() bool {
		messages := c.AllMessages()
		last := messages[len(messages)-1]
		for _, recipient := range last.Recipients {
			if last.RecipientDelivery[recipient] != core.DeliveryDelivered {
				return false
			}
		}
		return len(last.Recipients) > 0
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

func TestCodexCommentaryIsNotProjectedAsReply(t *testing.T) {
	c, _, _ := newScript(t, []string{})
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("ask both agents"); err != nil {
		t.Fatal(err)
	}
	emit := func(agent, session, turn, method string, kind runtime.EventKind, summary, phase string) {
		t.Helper()
		metadata := map[string]string{"backend_method": method}
		if phase != "" {
			metadata["message_phase"] = phase
		}
		if err := c.Observe(runtime.Event{
			Time: time.Now(), AgentID: agent, SessionID: session, TurnID: turn,
			Kind: kind, Summary: summary, Metadata: metadata,
		}); err != nil {
			t.Fatal(err)
		}
	}
	const turnID = "codex-turn-1"
	emit("shade", "thread-shade", turnID, "turn/started", runtime.KindStatus, "turn/started", "")
	emit("shade", "thread-shade", turnID, "item/agentMessage/delta", runtime.KindMessage, "Let me look first.", "")
	emit("shade", "thread-shade", turnID, "item/completed", runtime.KindStatus, "Let me look first.", "commentary")
	var buffered string
	_ = c.call(func(o *owner) { buffered = o.replyBuffers["thread-shade/"+turnID] })
	if buffered != "" {
		t.Fatalf("buffer after commentary=%q, want cleared", buffered)
	}
	emit("shade", "thread-shade", turnID, "item/agentMessage/delta", runtime.KindMessage, "The answer.", "")
	// An answer that completes with empty text falls back to its own deltas only.
	emit("shade", "thread-shade", turnID, "item/completed", runtime.KindMessage, "", "final_answer")
	emit("shade", "thread-shade", turnID, "turn/completed", runtime.KindStatus, "turn completed", "")
	emit("wraith", "thread-wraith", "codex-turn-2", "turn/started", runtime.KindStatus, "turn/started", "")
	emit("wraith", "thread-wraith", "codex-turn-2", "turn/completed", runtime.KindStatus, "turn completed", "")

	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Tasks) == 1 && workspace.Tasks[0].Status == core.TaskCompleted
	})
	replies := []string{}
	for _, message := range c.WorkspaceSnapshot().Messages {
		if message.Sender != core.HumanAgentID && message.Type == core.MessageReply {
			replies = append(replies, message.Content)
		}
	}
	if len(replies) != 1 || replies[0] != "The answer." {
		t.Fatalf("agent replies=%q, want only the final answer", replies)
	}
}
