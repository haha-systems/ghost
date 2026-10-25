package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

type brokerTestSession struct {
	*fake.Session
	mu     sync.Mutex
	inputs []runtime.Input
}

func (s *brokerTestSession) Send(ctx context.Context, input runtime.Input) error {
	s.mu.Lock()
	s.inputs = append(s.inputs, input)
	s.mu.Unlock()
	return s.Session.Send(ctx, input)
}

func (s *brokerTestSession) Steer(ctx context.Context, input runtime.Input) error {
	s.mu.Lock()
	s.inputs = append(s.inputs, input)
	s.mu.Unlock()
	return s.Session.Steer(ctx, input)
}

func (s *brokerTestSession) capturedInputs() []runtime.Input {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]runtime.Input(nil), s.inputs...)
}

func newBrokerTestController(t *testing.T, workdir string) (*Controller, map[string]*brokerTestSession) {
	t.Helper()
	agents := config.AgentSet{
		"wraith": {Runtime: "fake"},
		"veil":   {Runtime: "fake"},
		"shade":  {Runtime: "fake"},
	}
	c := New(config.Config{Agents: &agents, AgentOrder: []string{"wraith", "veil", "shade"}}, nil, Options{WorkDir: workdir})
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	sessions := map[string]*brokerTestSession{}
	installed := map[string]runtime.Session{}
	for _, name := range []string{"wraith", "veil", "shade"} {
		session := &brokerTestSession{Session: fake.NewSession(name)}
		sessions[name] = session
		installed[name] = session
	}
	if err := c.SetSessions(installed); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		for _, session := range sessions {
			_ = session.Close()
		}
	})
	return c, sessions
}

func TestControllerBrokerHumanDirectMessageStartsOnlyRecipient(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	if err := c.Send([]string{"wraith"}, "inspect the event bridge"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sessions["wraith"].capturedInputs()) == 1 })
	wraithInput := sessions["wraith"].capturedInputs()[0].Text
	if !strings.Contains(wraithInput, "inspect the event bridge") || !strings.Contains(wraithInput, "message_id:") || !strings.Contains(wraithInput, `sender: "you"`) {
		t.Fatalf("framed WRAITH input = %q", wraithInput)
	}
	if got := sessions["veil"].capturedInputs(); len(got) != 0 {
		t.Fatalf("VEIL received direct message: %#v", got)
	}
	workspace := c.WorkspaceSnapshot()
	if len(workspace.Messages) != 1 || workspace.Messages[0].RecipientDelivery[workspace.Agents[0].ID] != core.DeliveryDelivered {
		t.Fatalf("direct message projection = %#v", workspace.Messages)
	}
}

func TestControllerBrokerHumanBroadcastSharesMessageIDAndDeliversAll(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	if err := c.Send([]string{"all"}, "report current status"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return len(sessions["wraith"].capturedInputs()) == 1 && len(sessions["veil"].capturedInputs()) == 1
	})
	workspace := c.WorkspaceSnapshot()
	if len(workspace.Messages) != 1 {
		t.Fatalf("broadcast message count = %d", len(workspace.Messages))
	}
	message := workspace.Messages[0]
	for _, name := range []string{"wraith", "veil"} {
		input := sessions[name].capturedInputs()[0].Text
		if !strings.Contains(input, `message_id: "`+string(message.ID)+`"`) {
			t.Fatalf("%s input does not carry shared message ID %s: %q", name, message.ID, input)
		}
	}
	for _, agent := range workspace.Agents {
		if message.RecipientDelivery[agent.ID] != core.DeliveryDelivered {
			t.Fatalf("delivery to %s = %s", agent.DisplayName, message.RecipientDelivery[agent.ID])
		}
	}
}

func TestControllerBrokerOfflineDeliveryFailureIsVisible(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	if err := c.SetSessions(map[string]runtime.Session{"wraith": sessions["wraith"]}); err != nil {
		t.Fatal(err)
	}
	if err := c.Send([]string{"veil"}, "review the event bridge"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Messages) == 1 && workspace.Messages[0].Delivery == core.DeliveryPending
	})
	failed := fake.NewSession("veil")
	if err := failed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSessions(map[string]runtime.Session{"wraith": sessions["wraith"], "veil": failed}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Messages) == 1 && workspace.Messages[0].Delivery == core.DeliveryFailed
	})
	workspace := c.WorkspaceSnapshot()
	message := workspace.Messages[0]
	for recipient, reason := range message.DeliveryReason {
		if message.RecipientDelivery[recipient] == core.DeliveryFailed && reason == "" {
			t.Fatalf("failed delivery has no reason: %#v", message)
		}
	}
	if len(c.Events(EventFilter{Type: "message.failed"})) == 0 {
		t.Fatal("failed delivery event is missing")
	}
}

func TestControllerBrokerEventsAndMessagesHaveSingleSource(t *testing.T) {
	workdir := t.TempDir()
	c, _ := newBrokerTestController(t, workdir)
	if err := c.Send([]string{"wraith"}, "write one message"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(c.WorkspaceSnapshot().Messages) == 1 })
	if err := c.Observe(runtime.Event{AgentID: "wraith", SessionID: "wraith", TurnID: "fake-turn", Kind: runtime.KindTool, Summary: "provider telemetry only"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(c.Events(EventFilter{Category: core.CategoryTool})) > 0 })
	workspace := c.WorkspaceSnapshot()
	message := workspace.Messages[0]
	if len(workspace.Messages) != 1 || strings.Contains(workspace.Messages[0].Content, "provider telemetry") {
		t.Fatalf("provider telemetry entered the message projection: %#v", workspace.Messages)
	}
	accepted := 0
	for _, item := range workspace.Events {
		if item.Type != "message.accepted" {
			continue
		}
		var payload struct {
			Message core.Message `json:"message"`
		}
		if json.Unmarshal(item.Payload, &payload) == nil && payload.Message.ID == message.ID {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted events for message %s = %d", message.ID, accepted)
	}
	path := c.Status().EventLogPath
	if path == "" {
		t.Fatal("event log path is empty")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := core.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	logged := 0
	if err := log.Replay(func(item core.Event) error {
		if item.Type == "message.accepted" {
			var payload struct {
				Message core.Message `json:"message"`
			}
			if json.Unmarshal(item.Payload, &payload) == nil && payload.Message.ID == message.ID {
				logged++
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if logged != 1 {
		t.Fatalf("logged accepted events for message %s = %d", message.ID, logged)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	categories := map[core.Category]bool{}
	for _, item := range workspace.Events {
		categories[item.Category] = true
	}
	if !categories[core.CategoryConversation] || !categories[core.CategoryTask] || !categories[core.CategoryTool] || !categories[core.CategoryCoordination] {
		t.Fatalf("workspace event categories = %v", categories)
	}
}

func TestControllerBrokerCapabilityBriefIsInSessionInstructions(t *testing.T) {
	cfg := config.Default()
	agents := config.AgentSet{"wraith": {Runtime: "codex", WorkingDir: "."}, "veil": {Runtime: "codex", WorkingDir: "."}}
	cfg.Agents = &agents
	cfg.AgentOrder = []string{"wraith", "veil"}
	backend := fake.NewRuntime()
	c := New(cfg, nil, Options{Runtime: backend})
	t.Cleanup(func() { _ = c.Close() })
	sessions, failures := c.StartSessions()
	if len(failures) != 0 || len(sessions) != 2 {
		t.Fatalf("sessions=%v failures=%v", sessions, failures)
	}
	configs := backend.StartConfigs()
	if len(configs) != 2 {
		t.Fatalf("runtime starts = %d, want 2", len(configs))
	}
	for _, cfg := range configs {
		if !strings.Contains(cfg.Instructions, "ghost-message") || !strings.Contains(cfg.Instructions, "ghost-delegate") || !strings.Contains(cfg.Instructions, "Bare @mentions do not route") {
			t.Fatalf("session instructions lack channel brief: %q", cfg.Instructions)
		}
	}
	if len(c.Events(EventFilter{Type: "coordination.capability_briefed"})) != 2 {
		t.Fatal("capability brief is not visible in core events")
	}
}

func watchBrokerSessions(t *testing.T, c *Controller, sessions map[string]*brokerTestSession) {
	t.Helper()
	var group sync.WaitGroup
	for _, session := range sessions {
		group.Add(1)
		go func(session *brokerTestSession) {
			defer group.Done()
			for item := range session.Events() {
				_ = c.Observe(item)
			}
		}(session)
	}
	t.Cleanup(func() {
		for _, session := range sessions {
			_ = session.Close()
		}
		group.Wait()
	})
}

func waitForBroker(t *testing.T, c *Controller, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	workspace := c.WorkspaceSnapshot()
	tasks := make([]string, 0, len(workspace.Tasks))
	for _, task := range workspace.Tasks {
		parent := ""
		if task.Parent != nil {
			parent = string(*task.Parent)
		}
		tasks = append(tasks, fmt.Sprintf("%s parent=%s status=%s", task.ID, parent, task.Status))
	}
	messages := make([]string, 0, len(workspace.Messages))
	for _, message := range workspace.Messages {
		messages = append(messages, fmt.Sprintf("%s %s->%v %s", message.ID, message.Sender, message.Recipients, message.Delivery))
	}
	events := make([]string, 0)
	for _, item := range c.Events(EventFilter{Category: core.CategoryConversation}) {
		var payload struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(item.Payload, &payload)
		events = append(events, item.Type+" "+payload.Reason)
	}
	t.Fatalf("broker condition not met; tasks=%v messages=%v events=%v", tasks, messages, events)
}

func channelEvent(agent, text string) []runtime.Event {
	return []runtime.Event{
		{AgentID: agent, SessionID: agent, TurnID: "fake-turn", Kind: runtime.KindMessage, Summary: text, Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}},
		{AgentID: agent, SessionID: agent, TurnID: "fake-turn", Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}},
	}
}

func frameValue(frame, field string) string {
	for _, line := range strings.Split(frame, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if key == field && ok {
			var decoded string
			if json.Unmarshal([]byte(value), &decoded) == nil {
				return decoded
			}
		}
	}
	return ""
}

func TestControllerBrokerRoutesAgentMessageFromAuthenticatedSender(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	watchBrokerSessions(t, c, sessions)
	if err := c.Send([]string{"wraith"}, "start the review"); err != nil {
		t.Fatal(err)
	}
	task := c.WorkspaceSnapshot().Tasks[0]
	block := "```ghost-message\n" + `{"schema_version":1,"recipients":["veil"],"thread_id":"` + string(task.ThreadID) + `","task_id":"` + string(task.ID) + `","type":"direct","content":"Inspect the handler."}` + "\n```"
	sessions["wraith"].Emit(runtime.Event{AgentID: "wraith", SessionID: "wraith", TurnID: "fake-turn", Kind: runtime.KindMessage, Summary: block, Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}})
	waitForBroker(t, c, func() bool { return len(sessions["veil"].capturedInputs()) == 1 })
	workspace := c.WorkspaceSnapshot()
	var routed *core.Message
	for i := range workspace.Messages {
		message := &workspace.Messages[i]
		if message.Sender == task.Owner && message.Content == "Inspect the handler." {
			routed = message
		}
	}
	if routed == nil || len(routed.Recipients) != 1 || routed.Recipients[0] == task.Owner {
		t.Fatalf("routed agent message = %#v", routed)
	}
	frame := sessions["veil"].capturedInputs()[0].Text
	if !strings.Contains(frame, `sender: "WRAITH"`) || !strings.Contains(frame, `message_id: "`+string(routed.ID)+`"`) || !strings.Contains(frame, `task_id: "`+string(task.ID)+`"`) || !strings.Contains(frame, `thread_id: "`+string(task.ThreadID)+`"`) || !strings.Contains(frame, `type: "direct"`) || !strings.Contains(frame, "Inspect the handler.") {
		t.Fatalf("VEIL delivery frame = %q", frame)
	}
}

func TestControllerBrokerDelegationReturnsResultToParent(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	watchBrokerSessions(t, c, sessions)
	if err := c.call(func(o *owner) {
		wraith, _ := o.workspace.agentID("wraith")
		veil, _ := o.workspace.agentID("veil")
		o.workspace.mu.Lock()
		wraithAgent := o.workspace.agents[wraith]
		wraithAgent.PermissionPolicy = core.PermissionPolicy{Mode: core.PermissionModeAllowList, AllowTools: []string{"read", "write"}}
		o.workspace.agents[wraith] = wraithAgent
		veilAgent := o.workspace.agents[veil]
		veilAgent.PermissionPolicy = core.PermissionPolicy{Mode: core.PermissionModeAllowList, AllowTools: []string{"read", "shell"}}
		o.workspace.agents[veil] = veilAgent
		o.workspace.mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	var wraithSends atomic.Int32
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		if wraithSends.Add(1) == 1 {
			parent := frameValue(input.Text, "task_id")
			block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + parent + `","objective":"Review the event adapter","context_refs":[],"expected_deliverable":"Findings and one risk"}` + "\n```"
			return channelEvent("wraith", block)
		}
		return channelEvent("wraith", "I reviewed the returned findings.")
	}
	sessions["veil"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		child := frameValue(input.Text, "task_id")
		block := "```ghost-result\n" + `{"schema_version":1,"task_id":"` + child + `","status":"completed","result":"The adapter preserves event order."}` + "\n```"
		return channelEvent("veil", block)
	}
	if err := c.Send([]string{"wraith"}, "Review the event adapter."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Tasks) == 2 && workspace.Tasks[0].Status == core.TaskCompleted && workspace.Tasks[1].Status == core.TaskCompleted && wraithSends.Load() >= 2
	})
	workspace := c.WorkspaceSnapshot()
	parent, child := workspace.Tasks[0], workspace.Tasks[1]
	if child.Parent == nil || *child.Parent != parent.ID || child.Requester != parent.Owner || child.Owner == parent.Owner {
		t.Fatalf("delegated child = %#v, parent = %#v", child, parent)
	}
	if child.CESPolicy != parent.CESPolicy || child.Status != core.TaskCompleted {
		t.Fatalf("child policy/status = %s/%s; parent = %s", child.CESPolicy, child.Status, parent.CESPolicy)
	}
	if child.PermissionPolicy == nil || child.PermissionPolicy.Mode != core.PermissionModeAllowList || len(child.PermissionPolicy.AllowTools) != 1 || child.PermissionPolicy.AllowTools[0] != "read" {
		t.Fatalf("child permissions were not intersected: %#v", child.PermissionPolicy)
	}
	if parent.Status != core.TaskCompleted {
		t.Fatalf("parent task did not continue after child result: %s", parent.Status)
	}
	var delegation, result *core.Message
	for i := range workspace.Messages {
		message := &workspace.Messages[i]
		switch message.Type {
		case core.MessageTaskDelegation:
			delegation = message
		case core.MessageTaskResult:
			result = message
		}
	}
	if delegation == nil || delegation.RecipientDelivery[child.Owner] != core.DeliveryDelivered {
		t.Fatalf("delegation delivery = %#v", delegation)
	}
	frame := sessions["veil"].capturedInputs()[0].Text
	for _, field := range []string{`sender: "WRAITH"`, `message_id: "` + string(delegation.ID) + `"`, `task_id: "` + string(child.ID) + `"`, `type: "task_delegation"`, `objective: "Review the event adapter"`, `expected_deliverable: "Findings and one risk"`, `limits:`} {
		if !strings.Contains(frame, field) {
			t.Fatalf("delegation frame lacks %q: %q", field, frame)
		}
	}
	if result == nil || result.Sender != child.Owner || result.RecipientDelivery[parent.Owner] != core.DeliveryDelivered {
		t.Fatalf("task result message = %#v", result)
	}
	parentInputs := sessions["wraith"].capturedInputs()
	if len(parentInputs) != 2 || !strings.Contains(parentInputs[1].Text, `type: "task_result"`) || !strings.Contains(parentInputs[1].Text, "The adapter preserves event order.") {
		var rejected []string
		for _, item := range c.Events(EventFilter{Type: "message.rejected"}) {
			rejected = append(rejected, string(item.Payload))
		}
		t.Fatalf("parent session inputs = %#v; rejected = %v", parentInputs, rejected)
	}
	acceptedEvents := len(c.Events(EventFilter{Type: "message.accepted"}))
	taskCreatedEvents := len(c.Events(EventFilter{Type: "task.created"}))
	if acceptedEvents < 4 || taskCreatedEvents != 2 {
		t.Fatalf("broker event counts: accepted=%d task.created=%d messages=%#v", acceptedEvents, taskCreatedEvents, workspace.Messages)
	}
}

func TestControllerBrokerIgnoresBareMentionsAndRejectsSenderSpoofing(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	watchBrokerSessions(t, c, sessions)
	if err := c.Send([]string{"wraith"}, "inspect"); err != nil {
		t.Fatal(err)
	}
	task := c.WorkspaceSnapshot().Tasks[0]
	sessions["wraith"].Emit(runtime.Event{AgentID: "wraith", SessionID: "wraith", TurnID: "fake-turn", Kind: runtime.KindMessage, Summary: "I will ask @veil to review.", Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}})
	waitForBroker(t, c, func() bool { return len(c.WorkspaceSnapshot().Messages) >= 2 })
	for _, message := range c.AllMessages() {
		if message.Sender == task.Owner && len(message.Recipients) > 0 && message.Recipients[0] != core.HumanAgentID {
			t.Fatalf("bare mention routed as a message: %#v", message)
		}
	}
	spoof := "```ghost-message\n" + `{"schema_version":1,"sender":"veil","recipients":["veil"],"thread_id":"` + string(task.ThreadID) + `","type":"direct","content":"spoof"}` + "\n```"
	sessions["wraith"].Emit(runtime.Event{AgentID: "wraith", SessionID: "wraith", TurnID: "fake-turn", Kind: runtime.KindMessage, Summary: spoof, Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}})
	waitFor(t, func() bool { return len(c.Events(EventFilter{Type: "message.rejected"})) == 1 })
	if len(sessions["veil"].capturedInputs()) != 0 {
		t.Fatalf("spoofed sender reached VEIL: %#v", sessions["veil"].capturedInputs())
	}
	waitFor(t, func() bool { return strings.Contains(sessions["wraith"].LastSteeredText(), "Ghost rejected") })
}

func TestControllerBrokerCancelTaskCascadesAndInterruptsOwners(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	watchBrokerSessions(t, c, sessions)
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + parent + `","objective":"Review the event adapter","context_refs":[],"expected_deliverable":"Findings"}` + "\n```"
		return channelEvent("wraith", block)
	}
	sessions["veil"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"shade","parent_task_id":"` + parent + `","objective":"Check the event adapter","context_refs":[],"expected_deliverable":"One risk"}` + "\n```"
		return channelMessageEvent("veil", block)
	}
	if err := c.Send([]string{"wraith"}, "Review the event adapter."); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool { return len(c.WorkspaceSnapshot().Tasks) == 3 })
	workspace := c.WorkspaceSnapshot()
	root, child, grandchild := workspace.Tasks[0], workspace.Tasks[1], workspace.Tasks[2]
	if grandchild.Parent == nil || *grandchild.Parent != child.ID || child.Parent == nil || *child.Parent != root.ID {
		t.Fatalf("task tree = %#v", workspace.Tasks)
	}
	if err := c.CancelTask(child.ID); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		workspace := c.WorkspaceSnapshot()
		return workspace.Tasks[1].Status == core.TaskCancelled && workspace.Tasks[2].Status == core.TaskCancelled && sessions["veil"].LastInterruptedTurn() != ""
	})
	if sessions["veil"].LastInterruptedTurn() == "" {
		t.Fatal("VEIL was not interrupted")
	}
	notified := false
	for _, input := range sessions["wraith"].capturedInputs() {
		if strings.Contains(input.Text, `type: "task_result"`) && strings.Contains(input.Text, "cancelled:") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("WRAITH did not receive cancellation result: %#v", sessions["wraith"].capturedInputs())
	}
}

func TestControllerCancelParentRunCancelsOpenChildren(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	watchBrokerSessions(t, c, sessions)
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		if !strings.Contains(input.Text, "Ghost message v1") {
			return nil
		}
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + parent + `","objective":"Review the event adapter","context_refs":[],"expected_deliverable":"Findings"}` + "\n```"
		return channelEvent("wraith", block)
	}
	if err := c.Send([]string{"wraith"}, "Review the event adapter."); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Tasks) == 2 && sessions["veil"].State() == runtime.StateRunning
	})
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Tasks) == 2 && workspace.Tasks[0].Status == core.TaskCancelled && workspace.Tasks[1].Status == core.TaskCancelled && sessions["veil"].LastInterruptedTurn() != ""
	})
}

func TestControllerBrokerRejectsCyclicDelegation(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	watchBrokerSessions(t, c, sessions)
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + parent + `","objective":"Review the event adapter","context_refs":[],"expected_deliverable":"Findings"}` + "\n```"
		return channelEvent("wraith", block)
	}
	sessions["veil"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		if !strings.Contains(input.Text, "Ghost message v1") {
			return nil
		}
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"wraith","parent_task_id":"` + parent + `","objective":"Return to WRAITH","context_refs":[],"expected_deliverable":"A cycle"}` + "\n```"
		return channelEvent("veil", block)
	}
	if err := c.Send([]string{"wraith"}, "Review the event adapter."); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		for _, item := range c.Events(EventFilter{Type: "message.rejected"}) {
			if strings.Contains(string(item.Payload), "delegation cycle limit exceeded") {
				return true
			}
		}
		return false
	})
	if got := len(c.WorkspaceSnapshot().Tasks); got != 2 {
		t.Fatalf("cyclic delegation created %d tasks; want 2", got)
	}
	waitForBroker(t, c, func() bool { return strings.Contains(sessions["veil"].LastSentText(), "Ghost rejected") })
}

func TestControllerBrokerMessageBudgetStopsPingPong(t *testing.T) {
	c, sessions := newBrokerTestController(t, "")
	watchBrokerSessions(t, c, sessions)
	responder := func(agent, target string) func(runtime.Input) []runtime.Event {
		return func(input runtime.Input) []runtime.Event {
			if !strings.Contains(input.Text, "Ghost message v1") {
				return nil
			}
			taskID, threadID := frameValue(input.Text, "task_id"), frameValue(input.Text, "thread_id")
			block := "```ghost-message\n" + `{"schema_version":1,"recipients":["` + target + `"],"thread_id":"` + threadID + `","task_id":"` + taskID + `","type":"direct","content":"ping"}` + "\n```"
			return channelEvent(agent, block)
		}
	}
	sessions["wraith"].Session.OnSend = responder("wraith", "veil")
	sessions["wraith"].Session.OnSteer = responder("wraith", "veil")
	sessions["veil"].Session.OnSend = responder("veil", "wraith")
	sessions["veil"].Session.OnSteer = responder("veil", "wraith")
	if err := c.Send([]string{"all"}, "start ping pong"); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		for _, item := range c.Events(EventFilter{Type: "message.rejected"}) {
			if strings.Contains(string(item.Payload), "per-task message budget exhausted") {
				return true
			}
		}
		return false
	})
	if got := len(c.WorkspaceSnapshot().Messages); got != 50 {
		t.Fatalf("accepted messages after budget rejection = %d, want 50", got)
	}
}

func channelMessageEvent(agent, text string) []runtime.Event {
	return []runtime.Event{{AgentID: agent, SessionID: agent, TurnID: "fake-turn", Kind: runtime.KindMessage, Summary: text, Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}}}
}
