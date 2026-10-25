package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func validAgent() Agent {
	return Agent{
		ID: AgentID("agent_00000000000000010000000000000001"), DisplayName: "Shade",
		Runtime: RuntimeCodex, Availability: AvailabilityAvailable,
		PermissionPolicy: PermissionPolicy{Mode: PermissionModeAsk},
	}
}

func validTask() Task {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	return Task{
		ID:        TaskID("task_00000000000000010000000000000001"),
		Requester: AgentID("agent_00000000000000010000000000000001"),
		Owner:     AgentID("agent_00000000000000010000000000000002"),
		Status:    TaskQueued, CreatedAt: now, UpdatedAt: now,
		CESPolicy: CESOff, ThreadID: ThreadID("thread_00000000000000010000000000000001"),
	}
}

func validEvent() Event {
	return Event{
		ID: EventID("event_00000000000000010000000000000001"), SchemaVersion: EventSchemaVersion,
		Time:      time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		Workspace: WorkspaceID("workspace_00000000000000010000000000000001"),
		Actor:     Actor{Kind: ActorAgent, ID: "agent_00000000000000010000000000000001"},
		Source:    "runtime.codex", Type: "task.created", Category: CategoryTask,
		TaskID: TaskID("task_00000000000000010000000000000001"), Seq: 1,
		Payload: json.RawMessage(`{"goal":"inspect"}`),
	}
}

func TestIDGeneratorIsSortableUniqueAndInjectable(t *testing.T) {
	clock := time.Unix(100, 0)
	generator := NewIDGenerator(func() time.Time { return clock }, bytes.NewReader(bytes.Repeat([]byte{0x2a}, 32)))
	first, err := generator.New("task")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(-time.Second)
	second, err := generator.New("task")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first >= second {
		t.Fatalf("IDs are not unique and time ordered: %q, %q", first, second)
	}
	if err := (TaskID(first)).Validate(); err != nil {
		t.Fatalf("generated ID did not validate: %v", err)
	}
}

func TestIDValidationRejectsEmptyWrongPrefixAndMalformedValues(t *testing.T) {
	for name, id := range map[string]AgentID{
		"empty": "", "wrong prefix": "task_00000000000000010000000000000001",
		"malformed": "agent_not-an-id",
	} {
		t.Run(name, func(t *testing.T) {
			if err := id.Validate(); err == nil {
				t.Fatalf("Validate(%q) succeeded", id)
			}
		})
	}
}

func TestAgentValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if err := validAgent().Validate(); err != nil {
			t.Fatal(err)
		}
	})
	tests := []struct {
		name string
		edit func(*Agent)
	}{
		{"missing runtime", func(a *Agent) { a.Runtime = "" }},
		{"unknown availability", func(a *Agent) { a.Availability = "sleeping" }},
		{"duplicate capability", func(a *Agent) { a.Capabilities = []Capability{"shell", "shell"} }},
		{"invalid session binding", func(a *Agent) { a.SessionBinding = &SessionBinding{SessionID: "bad"} }},
		{"overlapping permission rules", func(a *Agent) {
			a.PermissionPolicy = PermissionPolicy{Mode: PermissionModeAllowList, AllowTools: []string{"shell"}, DenyTools: []string{"shell"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validAgent()
			tt.edit(&a)
			if err := a.Validate(); err == nil {
				t.Fatal("Validate succeeded for invalid agent")
			}
		})
	}
}

func TestTaskValidate(t *testing.T) {
	if err := validTask().Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*Task)
	}{
		{"missing owner", func(task *Task) { task.Owner = "" }},
		{"unknown status", func(task *Task) { task.Status = "paused" }},
		{"updated before creation", func(task *Task) { task.UpdatedAt = task.CreatedAt.Add(-time.Second) }},
		{"terminal without finish time", func(task *Task) { task.Status = TaskCompleted }},
		{"running without start time", func(task *Task) { task.Status = TaskRunning }},
		{"invalid dependency", func(task *Task) { task.DependsOn = []TaskID{"bad"} }},
		{"unknown CES policy", func(task *Task) { task.CESPolicy = "always" }},
		{"unknown CES phase", func(task *Task) { task.CESPolicy = CESEnforce; task.CESPhase = "decide" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := validTask()
			tt.edit(&task)
			if err := task.Validate(); err == nil {
				t.Fatal("Validate succeeded for invalid task")
			}
		})
	}
}

func TestHumanRequesterAndMessageIdentity(t *testing.T) {
	task := validTask()
	task.Requester = HumanAgentID
	if err := task.Validate(); err != nil {
		t.Fatalf("human task requester: %v", err)
	}

	message := Message{
		ID: MessageID("message_00000000000000010000000000000003"), Sender: HumanAgentID,
		Recipients: []AgentID{"agent_00000000000000010000000000000002"},
		ThreadID:   ThreadID("thread_00000000000000010000000000000001"),
		TaskID:     func() *TaskID { id := task.ID; return &id }(),
		Type:       MessageRequest, Content: "inspect this", CreatedAt: task.CreatedAt, Delivery: DeliveryPending,
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("human message sender: %v", err)
	}
	message.ID = MessageID("message_00000000000000010000000000000004")
	message.Sender = AgentID("agent_00000000000000010000000000000002")
	message.Recipients = []AgentID{HumanAgentID}
	message.Type = MessageReply
	message.ReplyTo = func() *MessageID { id := MessageID("message_00000000000000010000000000000003"); return &id }()
	if err := message.Validate(); err != nil {
		t.Fatalf("agent reply to human: %v", err)
	}
}

func TestMessageBroadcastSupportsExplicitRecipientsAndDeliveryMap(t *testing.T) {
	message := Message{
		ID:     MessageID("message_00000000000000010000000000000004"),
		Sender: AgentID("agent_00000000000000010000000000000001"),
		Recipients: []AgentID{
			AgentID("agent_00000000000000010000000000000002"),
			AgentID("agent_00000000000000010000000000000003"),
		},
		ThreadID: ThreadID("thread_00000000000000010000000000000001"),
		Type:     MessageBroadcast, Content: "status", CreatedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		Delivery: DeliveryPending,
		RecipientDelivery: map[AgentID]DeliveryStatus{
			AgentID("agent_00000000000000010000000000000002"): DeliveryDelivered,
			AgentID("agent_00000000000000010000000000000003"): DeliveryPending,
		},
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("broadcast with per-recipient state: %v", err)
	}

	var old Message
	if err := json.Unmarshal([]byte(`{"id":"message_00000000000000010000000000000005","sender":"you","recipients":["agent_00000000000000010000000000000002"],"thread_id":"thread_00000000000000010000000000000001","type":"direct","content":"old","created_at":"2026-10-25T12:00:00Z","delivery":"delivered"}`), &old); err != nil {
		t.Fatal(err)
	}
	if err := old.Validate(); err != nil {
		t.Fatalf("old message JSON no longer validates: %v", err)
	}
}

func TestTaskDelegationFieldsValidateAndRemainOptional(t *testing.T) {
	task := validTask()
	task.Depth = 2
	task.Objective = "review"
	task.ExpectedDeliverable = "findings"
	task.ContextRefs = []string{"note-1"}
	task.PermissionPolicy = &PermissionPolicy{Mode: PermissionModeAllowList, AllowTools: []string{"read"}}
	if err := task.Validate(); err != nil {
		t.Fatalf("delegated task: %v", err)
	}
	data, err := json.Marshal(validTask())
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"depth", "objective", "context_refs", "expected_deliverable", "permission_policy", "failure_reason", "correlation_id"} {
		if _, exists := decoded[key]; exists {
			t.Errorf("optional field %q was serialized for old task", key)
		}
	}
}

func TestTaskTransitions(t *testing.T) {
	now := validTask().CreatedAt.Add(time.Minute)
	tests := []struct {
		from TaskStatus
		to   TaskStatus
		ok   bool
	}{
		{TaskQueued, TaskRunning, true}, {TaskQueued, TaskWaiting, true},
		{TaskQueued, TaskCancelled, true}, {TaskQueued, TaskFailed, true},
		{TaskRunning, TaskWaiting, true}, {TaskRunning, TaskCompleted, true},
		{TaskRunning, TaskFailed, true}, {TaskRunning, TaskCancelled, true},
		{TaskWaiting, TaskRunning, true}, {TaskWaiting, TaskCompleted, true}, {TaskWaiting, TaskFailed, true},
		{TaskWaiting, TaskCancelled, true}, {TaskCompleted, TaskRunning, false},
		{TaskFailed, TaskCancelled, false}, {TaskCancelled, TaskQueued, false},
		{TaskQueued, TaskCompleted, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"_to_"+string(tt.to), func(t *testing.T) {
			task := validTask()
			task.Status = tt.from
			if tt.from == TaskRunning || tt.from == TaskWaiting {
				started := now.Add(-time.Second)
				task.StartedAt = &started
				task.UpdatedAt = started
			}
			if tt.from.IsTerminal() {
				finished := now.Add(-time.Second)
				task.FinishedAt = &finished
				task.UpdatedAt = finished
			}
			err := task.Transition(tt.to, now)
			if (err == nil) != tt.ok {
				t.Fatalf("Transition error = %v, want success %v", err, tt.ok)
			}
			if !tt.ok {
				return
			}
			if task.Status != tt.to || !task.UpdatedAt.Equal(now) {
				t.Fatalf("transitioned task = %#v", task)
			}
			if tt.to == TaskRunning && task.StartedAt == nil {
				t.Fatal("running task has no start time")
			}
			if tt.to.IsTerminal() && (task.FinishedAt == nil || !task.FinishedAt.Equal(now)) {
				t.Fatal("terminal task has no finish time")
			}
		})
	}
}

func TestMessageTypeRules(t *testing.T) {
	base := Message{ID: MessageID("message_00000000000000010000000000000001"), Sender: AgentID("agent_00000000000000010000000000000001"), ThreadID: ThreadID("thread_00000000000000010000000000000001"), Content: "hello", CreatedAt: time.Now(), Delivery: DeliveryPending}
	tests := []struct {
		name string
		edit func(*Message)
		ok   bool
	}{
		{"direct", func(m *Message) {
			m.Type = MessageDirect
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002"}
		}, true},
		{"broadcast empty recipients", func(m *Message) { m.Type = MessageBroadcast }, true},
		{"broadcast wildcard", func(m *Message) { m.Type = MessageBroadcast; m.Recipients = []AgentID{"*"} }, true},
		{"broadcast wildcard mixed", func(m *Message) {
			m.Type = MessageBroadcast
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002", "*"}
		}, false},
		{"broadcast explicit recipients", func(m *Message) {
			m.Type = MessageBroadcast
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002"}
		}, true},
		{"request needs recipient", func(m *Message) { m.Type = MessageRequest }, false},
		{"reply needs reply target", func(m *Message) {
			m.Type = MessageReply
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002"}
		}, false},
		{"reply valid", func(m *Message) {
			m.Type = MessageReply
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002"}
			id := MessageID("message_00000000000000010000000000000002")
			m.ReplyTo = &id
		}, true},
		{"delegation needs task", func(m *Message) {
			m.Type = MessageTaskDelegation
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002"}
		}, false},
		{"task result needs task", func(m *Message) { m.Type = MessageTaskResult }, false},
		{"delegation valid", func(m *Message) {
			m.Type = MessageTaskDelegation
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002"}
			id := TaskID("task_00000000000000010000000000000001")
			m.TaskID = &id
		}, true},
		{"task result valid", func(m *Message) {
			m.Type = MessageTaskResult
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002"}
			id := TaskID("task_00000000000000010000000000000001")
			m.TaskID = &id
		}, true},
		{"duplicate recipients", func(m *Message) {
			m.Type = MessageDirect
			m.Recipients = []AgentID{"agent_00000000000000010000000000000002", "agent_00000000000000010000000000000002"}
		}, false},
		{"system may omit sender", func(m *Message) { m.Type = MessageSystem; m.Sender = "" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			tt.edit(&m)
			if err := m.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate error = %v, want success %v", err, tt.ok)
			}
		})
	}
}

func TestCategoryOf(t *testing.T) {
	for eventType, want := range map[string]Category{
		"message.created": CategoryConversation, "conversation.reply": CategoryConversation,
		"task.status_changed": CategoryTask, "tool.started": CategoryTool,
		"coordination.assigned": CategoryCoordination, "memory.recalled": CategoryMemory,
		"decision.qac_selected": CategoryDecision,
	} {
		if got := CategoryOf(eventType); got != want {
			t.Errorf("CategoryOf(%q) = %q, want %q", eventType, got, want)
		}
	}
	if got := CategoryOf("unknown.kind"); got != "" {
		t.Fatalf("unknown category = %q", got)
	}
}

func TestEventValidateAndJSONRoundTripAllowsUnknownFields(t *testing.T) {
	event := validEvent()
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Event
	if err := json.Unmarshal(append(encoded[:len(encoded)-1], []byte(`,"future_field":"ignored"}`)...), &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.ID != event.ID || roundTrip.Type != event.Type || roundTrip.Seq != event.Seq || string(roundTrip.Payload) != string(event.Payload) {
		t.Fatalf("round trip mismatch: %#v", roundTrip)
	}
	tests := []struct {
		name string
		edit func(*Event)
	}{
		{"version", func(e *Event) { e.SchemaVersion++ }},
		{"category mismatch", func(e *Event) { e.Category = CategoryTool }},
		{"unclassified type", func(e *Event) { e.Type = "unknown.event" }},
		{"invalid sequence", func(e *Event) { e.Seq = 0 }},
		{"invalid payload", func(e *Event) { e.Payload = []byte("{") }},
		{"invalid correlation", func(e *Event) { e.CorrelationID = "bad" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := validEvent()
			tt.edit(&bad)
			if err := bad.Validate(); err == nil {
				t.Fatal("Validate succeeded for invalid event")
			}
		})
	}
}

func TestEventStreamKeyAndSequencer(t *testing.T) {
	event := validEvent()
	if !strings.Contains(event.StreamKey(), string(event.TaskID)) {
		t.Fatalf("task stream key = %q", event.StreamKey())
	}
	event.TaskID = ""
	event.SessionID = SessionID("session_00000000000000010000000000000001")
	if !strings.Contains(event.StreamKey(), string(event.SessionID)) {
		t.Fatalf("session stream key = %q", event.StreamKey())
	}
	seq := NewSequencer()
	var wg sync.WaitGroup
	const workers, perWorker = 16, 40
	values := make(chan uint64, workers*perWorker)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				e := validEvent()
				assigned, err := seq.Assign(e)
				if err != nil {
					t.Errorf("Assign: %v", err)
					return
				}
				values <- assigned.Seq
			}
		}()
	}
	wg.Wait()
	close(values)
	seen := make([]int, 0, workers*perWorker)
	for value := range values {
		seen = append(seen, int(value))
	}
	sort.Ints(seen)
	for i, value := range seen {
		if value != i+1 {
			t.Fatalf("sequence[%d] = %d", i, value)
		}
	}
}

func TestEventLogAppendReplayAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	first, err := log.Append(validEvent())
	if err != nil {
		t.Fatal(err)
	}
	secondInput := validEvent()
	secondInput.ID = EventID("event_00000000000000010000000000000002")
	second, err := log.Append(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("appended sequences = %d, %d", first.Seq, second.Seq)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`{"broken":`)
	_ = file.Close()
	log, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := log.Recovery().SkippedTailLines; got != 1 {
		t.Fatalf("skipped tail lines = %d, want 1", got)
	}
	thirdInput := validEvent()
	thirdInput.ID = EventID("event_00000000000000010000000000000003")
	third, err := log.Append(thirdInput)
	if err != nil {
		t.Fatal(err)
	}
	if third.Seq != 3 {
		t.Fatalf("sequence after recovery = %d, want 3", third.Seq)
	}
	var replayed []Event
	if err := log.Replay(func(event Event) error { replayed = append(replayed, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 3 || replayed[2].Seq != 3 {
		t.Fatalf("replayed events = %#v", replayed)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEventLogRejectsCorruptionBeforeFinalLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	data := fmt.Sprintf("%s\nnot-json\n%s\n", mustJSON(t, validEvent()), mustJSON(t, validEvent()))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted corruption before the final line")
	}
}

func TestEventLogSkipsCorruptFinalLineWithNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tail.jsonl")
	data := mustJSON(t, validEvent()) + "\nnot-json\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if got := log.Recovery().SkippedTailLines; got != 1 {
		t.Fatalf("skipped tail lines = %d, want 1", got)
	}
	var count int
	if err := log.Replay(func(Event) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("replayed %d events, want 1", count)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNewIDRejectsInvalidPrefix(t *testing.T) {
	for _, prefix := range []string{"", "has space", "_bad", "Upper"} {
		if _, err := NewID(prefix); err == nil {
			t.Errorf("NewID(%q) succeeded", prefix)
		}
	}
}
