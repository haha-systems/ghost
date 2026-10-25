package run

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func TestWorkspaceProjectionUsesConfiguredAgentOrder(t *testing.T) {
	agents := config.AgentSet{
		"shade":  {Runtime: "codex"},
		"wraith": {Runtime: "codex"},
	}
	cfg := config.Default()
	cfg.Agents = &agents
	cfg.AgentOrder = []string{"wraith", "shade"}
	controller := New(cfg, nil, Options{})
	t.Cleanup(func() { _ = controller.Close() })

	snapshot := controller.WorkspaceSnapshot()
	got := make([]string, 0, len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		got = append(got, agent.DisplayName)
	}
	if want := []string{"wraith", "shade"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace agents = %v, want %v", got, want)
	}
}

func TestWorkspaceProjectionFallsBackToAlphabeticalAgentOrder(t *testing.T) {
	agents := config.AgentSet{
		"shade":  {Runtime: "codex"},
		"wraith": {Runtime: "codex"},
	}
	cfg := config.Default()
	cfg.Agents = &agents
	controller := New(cfg, nil, Options{})
	t.Cleanup(func() { _ = controller.Close() })

	snapshot := controller.WorkspaceSnapshot()
	got := make([]string, 0, len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		got = append(got, agent.DisplayName)
	}
	if want := []string{"shade", "wraith"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace agents = %v, want %v", got, want)
	}
}

func TestWorkspaceMessagesAndTasksAreSingleProjections(t *testing.T) {
	store := testWorkspace(t)
	shade, _ := store.agentID("shade")
	wraith, _ := store.agentID("wraith")
	task := testWorkspaceTask(t, store, "shade")
	task.Participants = append(task.Participants, wraith)
	if err := store.addTask(task); err != nil {
		t.Fatal(err)
	}

	human := testWorkspaceMessage(t, task, core.HumanAgentID, []core.AgentID{shade}, core.MessageRequest, "inspect the event bridge")
	if err := store.addMessage(human); err != nil {
		t.Fatal(err)
	}
	reply := testWorkspaceMessage(t, task, shade, []core.AgentID{core.HumanAgentID}, core.MessageReply, "the bridge preserves the payload")
	reply.ReplyTo = &human.ID
	if err := store.addMessage(reply); err != nil {
		t.Fatal(err)
	}
	broadcast := testWorkspaceMessage(t, task, core.HumanAgentID, []core.AgentID{core.BroadcastWildcard}, core.MessageBroadcast, "status update")
	if err := store.addMessage(broadcast); err != nil {
		t.Fatal(err)
	}

	if got := len(store.messagesForAgent("shade")); got != 3 {
		t.Fatalf("shade message count = %d", got)
	}
	if got := len(store.messagesForAgent(string(wraith))); got != 1 {
		t.Fatalf("wraith message count = %d", got)
	}
	if got := len(store.allMessages()); got != 3 {
		t.Fatalf("messages stored = %d, want 3", got)
	}
	if got := len(store.tasksForAgent("wraith")); got != 1 {
		t.Fatalf("wraith task count = %d", got)
	}

	copy := store.messagesForAgent("shade")
	copy[0].Recipients[0] = wraith
	if store.allMessages()[0].Recipients[0] != shade {
		t.Fatal("message query returned shared mutable data")
	}
}

func TestWorkspaceMentionsResolveRecipientsAndBroadcast(t *testing.T) {
	store := testWorkspace(t)
	got, broadcast, err := store.resolveRecipients(nil, "Review @shade and @wraith")
	if err != nil || broadcast || len(got) != 2 {
		t.Fatalf("resolved mentions = %v, broadcast=%v, err=%v", got, broadcast, err)
	}
	got, broadcast, err = store.resolveRecipients([]string{"wraith"}, "@shade please check")
	if err != nil || broadcast || len(got) != 2 {
		t.Fatalf("merged recipients = %v, broadcast=%v, err=%v", got, broadcast, err)
	}
	got, broadcast, err = store.resolveRecipients(nil, "@all status")
	if err != nil || !broadcast || len(got) != 1 || got[0] != core.BroadcastWildcard {
		t.Fatalf("broadcast recipients = %v, broadcast=%v, err=%v", got, broadcast, err)
	}
	if _, _, err := store.resolveRecipients(nil, "ask @missing"); err == nil {
		t.Fatal("unknown mention was accepted")
	}
}

func TestWorkspaceEventsFilterAndSnapshotAreDetached(t *testing.T) {
	store := testWorkspace(t)
	shade, _ := store.agentID("shade")
	workspace := store.workspaceID()
	for i, text := range []string{"agent reply", "tool summary"} {
		id, _ := core.NewID("event")
		typeName, category := "conversation.agent_message", core.CategoryConversation
		if i == 1 {
			typeName, category = "tool.call", core.CategoryTool
		}
		payload, _ := json.Marshal(map[string]string{"detail": text})
		if err := store.addEvent(core.Event{ID: core.EventID(id), SchemaVersion: core.EventSchemaVersion, Time: time.Now(), Workspace: workspace, Actor: core.Actor{Kind: core.ActorAgent, ID: string(shade)}, Source: "runtime", Type: typeName, Category: category, AgentID: shade, Seq: uint64(i + 1), Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	got := store.events(EventFilter{Agent: "shade", Category: core.CategoryTool, Search: "summary"})
	if len(got) != 1 || got[0].Type != "tool.call" {
		t.Fatalf("filtered events = %#v", got)
	}
	got[0].Payload[0] = 'x'
	if string(store.events(EventFilter{Type: "tool.call"})[0].Payload) == string(got[0].Payload) {
		t.Fatal("event query returned shared payload bytes")
	}
	snapshot := store.snapshot()
	snapshot.Agents[0].DisplayName = "changed"
	if store.snapshot().Agents[0].DisplayName == "changed" {
		t.Fatal("workspace snapshot returned shared agent data")
	}
}

func TestWorkspaceAgentSessionAndTaskTransitions(t *testing.T) {
	store := testWorkspace(t)
	session := fake.NewSession("shade")
	sessionID, err := store.updateAgentSession("shade", session, "")
	if err != nil {
		t.Fatal(err)
	}
	task := testWorkspaceTask(t, store, "shade")
	if err := store.addTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.transitionTask(task.ID, core.TaskRunning, task.CreatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.assignTask(task.ID, "wraith"); err != nil {
		t.Fatal(err)
	}
	snapshot := store.snapshot()
	for _, agent := range snapshot.Agents {
		if agent.DisplayName == "shade" && (agent.SessionBinding == nil || agent.SessionBinding.SessionID != sessionID || agent.Availability != core.AvailabilityAvailable) {
			t.Fatalf("session agent state = %#v", agent)
		}
	}
	if got := store.tasksForAgent("wraith"); len(got) != 1 || got[0].Status != core.TaskRunning || got[0].Owner == task.Owner {
		t.Fatalf("assigned task = %#v", got)
	}
}

func TestWorkspaceConcurrentReadsAndWrites(t *testing.T) {
	store := testWorkspace(t)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 25 {
				_ = store.snapshot()
				_ = store.allMessages()
				_ = store.events(EventFilter{})
			}
		}()
	}
	for range 25 {
		task := testWorkspaceTask(t, store, "shade")
		if err := store.addTask(task); err != nil {
			t.Fatal(err)
		}
		message := testWorkspaceMessage(t, task, core.HumanAgentID, []core.AgentID{task.Owner}, core.MessageRequest, "check")
		if err := store.addMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	workers.Wait()
}

func testWorkspace(t *testing.T) *workspaceProjection {
	t.Helper()
	id, err := core.NewID("workspace")
	if err != nil {
		t.Fatal(err)
	}
	store, err := newWorkspaceProjection(core.WorkspaceID(id), config.AgentSet{
		"shade": {Runtime: "codex"}, "wraith": {Runtime: "codex"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testWorkspaceTask(t *testing.T, store *workspaceProjection, owner string) core.Task {
	t.Helper()
	id, err := core.NewID("task")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := core.NewID("thread")
	if err != nil {
		t.Fatal(err)
	}
	ownerID, ok := store.agentID(owner)
	if !ok {
		t.Fatalf("unknown test owner %q", owner)
	}
	now := time.Now().UTC()
	return core.Task{ID: core.TaskID(id), Requester: core.HumanAgentID, Owner: ownerID, Participants: []core.AgentID{ownerID}, Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now, CESPolicy: core.CESOff, ThreadID: core.ThreadID(thread)}
}

func testWorkspaceMessage(t *testing.T, task core.Task, sender core.AgentID, recipients []core.AgentID, kind core.MessageType, content string) core.Message {
	t.Helper()
	id, err := core.NewID("message")
	if err != nil {
		t.Fatal(err)
	}
	taskID := task.ID
	return core.Message{ID: core.MessageID(id), Sender: sender, Recipients: recipients, ThreadID: task.ThreadID, TaskID: &taskID, Type: kind, Content: content, CreatedAt: time.Now().UTC(), Delivery: core.DeliveryDelivered}
}
