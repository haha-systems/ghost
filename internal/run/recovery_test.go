package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/memory"
	"github.com/haha-systems/ghost/internal/memory/memorytest"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func TestControllerRestartRestoresHistoryAndRetriesOnlyPurePendingMessages(t *testing.T) {
	workdir := t.TempDir()
	cfg := recoveryConfig()
	first, firstSessions := recoveryController(t, workdir, cfg, "wraith", "shade")
	if err := first.Send([]string{"wraith"}, "human message survives restart"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(first.AllMessages()) == 1 })
	root := first.WorkspaceSnapshot().Tasks[0]

	observeRecoveryChannel(t, first, "wraith", root, "ghost-message", map[string]any{
		"recipients": []string{"shade"}, "type": "direct", "content": "delivered agent message",
	})
	observeRecoveryChannel(t, first, "wraith", root, "ghost-message", map[string]any{
		"recipients": []string{"veil"}, "type": "direct", "content": "pending pure message",
	})
	observeRecoveryChannel(t, first, "wraith", root, "ghost-delegate", map[string]any{
		"target": "veil", "parent_task_id": root.ID, "objective": "inspect the change",
		"context_refs": []string{}, "expected_deliverable": "a review",
	})
	waitFor(t, func() bool {
		return len(first.AllMessages()) == 4 && len(first.WorkspaceSnapshot().Tasks) == 2
	})
	oldMessages := map[string]core.Message{}
	for _, message := range first.AllMessages() {
		oldMessages[message.Content] = message
	}
	if oldMessages["human message survives restart"].Delivery != core.DeliveryDelivered ||
		oldMessages["delivered agent message"].Delivery != core.DeliveryDelivered ||
		oldMessages["pending pure message"].Delivery != core.DeliveryPending {
		t.Fatalf("first controller message states: %#v", oldMessages)
	}
	var child core.Task
	for _, task := range first.WorkspaceSnapshot().Tasks {
		if task.Parent != nil {
			child = task
		}
	}
	if child.ID == "" {
		t.Fatal("delegation child was not created")
	}
	oldPath := first.Status().EventLogPath
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	for _, session := range firstSessions {
		_ = session.Close()
	}
	oldFile, err := os.OpenFile(oldPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldFile.WriteString(`{"truncated":`); err != nil {
		t.Fatal(err)
	}
	if err := oldFile.Close(); err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join(workdir, ".ghost", "events", "000-corrupt.jsonl")
	corruptBytes := []byte("not-json\n{}\n")
	if err := os.WriteFile(corruptPath, corruptBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	second := New(cfg, nil, Options{WorkDir: workdir})
	t.Cleanup(func() { _ = second.Close() })
	beforeAvailability := second.WorkspaceSnapshot()
	if len(beforeAvailability.Messages) != 5 || len(beforeAvailability.Tasks) != 2 {
		t.Fatalf("restored records: messages=%d tasks=%d", len(beforeAvailability.Messages), len(beforeAvailability.Tasks))
	}
	restored := map[string]core.Message{}
	for _, message := range beforeAvailability.Messages {
		restored[message.Content] = message
	}
	for _, content := range []string{"human message survives restart", "delivered agent message"} {
		if got := restored[content].Delivery; got != core.DeliveryDelivered {
			t.Errorf("%q delivery = %s, want delivered", content, got)
		}
	}
	if got := restored["pending pure message"].Delivery; got != core.DeliveryPending {
		t.Fatalf("pending pure message delivery = %s", got)
	}
	if got := restored["inspect the change"].RecipientDelivery[restored["inspect the change"].Recipients[0]]; got != core.DeliveryFailed {
		t.Fatalf("delegation delivery state = %s, want failed", got)
	}
	if got := restored["inspect the change"].DeliveryReason[restored["inspect the change"].Recipients[0]]; got != "uncertain after restart" {
		t.Fatalf("delegation delivery reason = %q", got)
	}
	for _, task := range beforeAvailability.Tasks {
		if task.Status != core.TaskFailed || task.FailureReason != "interrupted: ghost restarted" {
			t.Errorf("task %s recovery state = %s (%q)", task.ID, task.Status, task.FailureReason)
		}
		if task.ID == child.ID && (task.Parent == nil || *task.Parent != root.ID) {
			t.Errorf("restored child parent = %v, want %s", task.Parent, root.ID)
		}
	}
	var childCascade bool
	for _, item := range beforeAvailability.Events {
		if item.Type != "task.reconciled" {
			continue
		}
		var payload struct {
			TaskID  core.TaskID `json:"task_id"`
			Cascade bool        `json:"cascade"`
		}
		if json.Unmarshal(item.Payload, &payload) == nil && payload.TaskID == child.ID {
			childCascade = payload.Cascade
		}
	}
	if !childCascade {
		t.Fatal("interrupted child task lacks cascade reconciliation")
	}
	if !containsRunEvent(beforeAvailability.Events, "workspace.recovered") || !containsRunEvent(beforeAvailability.Events, "task.reconciled") {
		t.Fatal("recovery summary or task reconciliation event is missing")
	}
	if second.Status().Recovery.SkippedTailLines != 1 || second.Status().Recovery.FilesSkipped != 1 {
		t.Fatalf("corruption recovery summary = %#v", second.Status().Recovery)
	}
	var visibleWarning bool
	for _, item := range beforeAvailability.Events {
		if item.Type == "workspace.recovery_warning" && strings.Contains(string(item.Payload), "000-corrupt.jsonl") {
			visibleWarning = true
		}
	}
	if !visibleWarning {
		t.Fatal("mid-file corruption did not create a visible warning event")
	}

	secondSessions := map[string]*brokerTestSession{}
	installed := map[string]runtime.Session{}
	for _, name := range []string{"wraith", "veil", "shade"} {
		session := &brokerTestSession{Session: fake.NewSession(name)}
		secondSessions[name] = session
		installed[name] = session
		t.Cleanup(func() { _ = session.Close() })
	}
	if err := second.SetSessions(installed); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		pureDelivered, requesterNotified := false, false
		for _, message := range second.AllMessages() {
			if message.Content == "pending pure message" && message.Delivery == core.DeliveryDelivered {
				pureDelivered = true
			}
			if message.Type == core.MessageSystem && strings.Contains(message.Content, "Ghost restarted") && message.Delivery == core.DeliveryDelivered {
				requesterNotified = true
			}
		}
		return pureDelivered && requesterNotified
	})
	veilInputs := secondSessions["veil"].capturedInputs()
	if len(veilInputs) != 1 || !strings.Contains(veilInputs[0].Text, "pending pure message") {
		t.Fatalf("VEIL received %d inputs after recovery: %#v", len(veilInputs), veilInputs)
	}
	wraithInputs := secondSessions["wraith"].capturedInputs()
	if len(wraithInputs) != 1 || !strings.Contains(wraithInputs[0].Text, "Ghost restarted") {
		t.Fatalf("WRAITH did not receive the uncertain-work notice: %#v", wraithInputs)
	}
	for _, message := range []core.Message{oldMessages["human message survives restart"], oldMessages["delivered agent message"]} {
		for _, session := range secondSessions {
			for _, input := range session.capturedInputs() {
				if strings.Contains(input.Text, string(message.ID)) {
					t.Errorf("delivered message %s was sent again", message.ID)
				}
			}
		}
	}
	for _, session := range secondSessions {
		for _, input := range session.capturedInputs() {
			if strings.Contains(input.Text, string(oldMessages["inspect the change"].ID)) {
				t.Fatal("uncertain delegation was delivered again")
			}
		}
	}
	newPath := second.Status().EventLogPath
	if oldPath == newPath {
		t.Fatal("restart appended to the old run file")
	}
	unchanged, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(oldBytes) {
		t.Fatal("restart changed an old run file")
	}
	unchangedCorruption, err := os.ReadFile(corruptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchangedCorruption) != string(corruptBytes) {
		t.Fatal("restart changed a skipped corrupt file")
	}
	assertNewRunIDsAndSequences(t, oldPath, newPath)
}

func TestControllerRestartMarksUnfinishedMemoryWriteLostWithoutResending(t *testing.T) {
	workdir := t.TempDir()
	cfg := recoveryConfig()
	first, sessions := recoveryController(t, workdir, cfg, "wraith")
	if err := first.Send([]string{"wraith"}, "task with unfinished memory write"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(first.AllMessages()) == 1 })
	task := first.WorkspaceSnapshot().Tasks[0]
	var eventErr error
	if err := first.call(func(o *owner) {
		ctx := o.eventContext()
		ctx.TaskID, ctx.ThreadID = task.ID, task.ThreadID
		var item core.Event
		item, eventErr = newCoreEvent(ctx, timeNow(), core.Actor{Kind: core.ActorSystem, ID: "memory"}, "memory", core.MemoryEventWriteQueued, task.Owner, "", map[string]any{
			"write_id": "write-before-restart", "scope": "workspace", "claim_kind": "inference", "summary": "unfinished result",
		})
		if eventErr == nil {
			o.publishCoreEvent(item)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if eventErr != nil {
		t.Fatal(eventErr)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		_ = session.Close()
	}

	backend := memorytest.New()
	factory := func(workspace core.WorkspaceID, agents map[string]core.AgentID, sink func(core.Event)) MemoryService {
		authorized := make([]core.AgentID, 0, len(agents))
		for _, id := range agents {
			authorized = append(authorized, id)
		}
		return memory.NewService(backend, memory.Config{Enabled: true, Workspace: workspace, WorkspaceSpace: "workspace-space", AuthorizedAgents: authorized}, memory.EventSink(sink))
	}
	second := New(cfg, nil, Options{WorkDir: workdir, MemoryFactory: factory})
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	var lost core.Event
	for _, item := range second.Events(EventFilter{Category: core.CategoryMemory}) {
		if item.Type == core.MemoryEventWriteLost {
			lost = item
		}
	}
	var payload struct {
		WriteID string `json:"write_id"`
		Reason  string `json:"reason"`
	}
	if lost.ID == "" || json.Unmarshal(lost.Payload, &payload) != nil || payload.WriteID != "write-before-restart" || payload.Reason != "ghost restarted before write completed" {
		t.Fatalf("lost write event = %#v payload=%#v", lost, payload)
	}
	if second.Status().Recovery.Memory.WritesQueued != 1 || second.Status().Recovery.Memory.WritesLost != 1 || len(backend.WriteRecords()) != 0 {
		t.Fatalf("recovered memory counts=%#v; backend writes=%#v", second.Status().Recovery.Memory, backend.WriteRecords())
	}
}

func TestControllerRestartRestoresMemoryHistoryForInspector(t *testing.T) {
	workdir := t.TempDir()
	cfg := recoveryConfig()
	first, sessions := recoveryController(t, workdir, cfg, "wraith")
	var eventErr error
	if err := first.call(func(o *owner) {
		var item core.Event
		item, eventErr = newCoreEvent(o.eventContext(), timeNow(), core.Actor{Kind: core.ActorSystem, ID: "memory"}, "memory", core.MemoryEventRetrievalCompleted, "", "", map[string]any{
			"reason": "task_started", "latency_ms": 3, "returned": 1,
			"record_ids":      []string{"recovered-memory"},
			"record_metadata": []map[string]any{{"id": "recovered-memory", "scope": core.MemoryScopeWorkspace, "content_bytes": 20}},
		})
		if eventErr == nil {
			o.publishCoreEvent(item)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if eventErr != nil {
		t.Fatal(eventErr)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		_ = session.Close()
	}

	second := New(cfg, nil, Options{WorkDir: workdir})
	t.Cleanup(func() { _ = second.Close() })
	history := second.WorkspaceSnapshot().Memory
	if len(history.Events) != 1 || history.Events[0].Type != core.MemoryEventRetrievalCompleted || !strings.Contains(string(history.Events[0].Payload), "recovered-memory") || strings.Contains(string(history.Events[0].Payload), "restored memory record") {
		t.Fatalf("recovered memory history = %#v", history)
	}
	if history.Counters.RetrievalLatencyMS != 3 {
		t.Fatalf("recovered memory counters = %#v", history.Counters)
	}
}

func recoveryConfig() config.Config {
	agents := config.AgentSet{
		"wraith": {Runtime: "fake"}, "veil": {Runtime: "fake"}, "shade": {Runtime: "fake"},
	}
	return config.Config{Agents: &agents, AgentOrder: []string{"wraith", "veil", "shade"}}
}

func recoveryController(t *testing.T, workdir string, cfg config.Config, names ...string) (*Controller, map[string]*brokerTestSession) {
	t.Helper()
	c := New(cfg, nil, Options{WorkDir: workdir})
	sessions := map[string]*brokerTestSession{}
	installed := map[string]runtime.Session{}
	for _, name := range names {
		session := &brokerTestSession{Session: fake.NewSession(name)}
		sessions[name] = session
		installed[name] = session
	}
	if err := c.SetSessions(installed); err != nil {
		t.Fatal(err)
	}
	return c, sessions
}

func observeRecoveryChannel(t *testing.T, c *Controller, agent string, task core.Task, fence string, fields map[string]any) {
	t.Helper()
	fields["schema_version"] = 1
	if fence == "ghost-message" {
		fields["thread_id"] = task.ThreadID
		fields["task_id"] = task.ID
	}
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	text := "```" + fence + "\n" + string(body) + "\n```"
	if err := c.Observe(runtime.Event{AgentID: agent, SessionID: agent, TurnID: "fake-turn", Kind: runtime.KindMessage, Summary: text, Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}}); err != nil {
		t.Fatal(err)
	}
}

func containsRunEvent(events []core.Event, eventType string) bool {
	for _, item := range events {
		if item.Type == eventType {
			return true
		}
	}
	return false
}

func assertNewRunIDsAndSequences(t *testing.T, oldPath, newPath string) {
	t.Helper()
	read := func(path string) []core.Event {
		var events []core.Event
		if _, err := core.ReadOnlyReplay(path, func(item core.Event) error {
			events = append(events, item)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return events
	}
	oldEvents, newEvents := read(oldPath), read(newPath)
	ids := map[core.EventID]bool{}
	sequences := map[string]uint64{}
	for _, item := range oldEvents {
		ids[item.ID] = true
		if item.Seq > sequences[item.StreamKey()] {
			sequences[item.StreamKey()] = item.Seq
		}
	}
	for _, item := range newEvents {
		if ids[item.ID] {
			t.Errorf("new event reused old ID %s", item.ID)
		}
		if item.Seq <= sequences[item.StreamKey()] {
			t.Errorf("stream %s sequence %d did not continue past %d", item.StreamKey(), item.Seq, sequences[item.StreamKey()])
		}
		sequences[item.StreamKey()] = item.Seq
	}
}

func TestRecoveryConfigUsesExpectedNames(t *testing.T) {
	cfg := recoveryConfig()
	if got := len(*cfg.Agents); got != 3 {
		t.Fatalf("recovery agents = %d", got)
	}
}

func TestRecoveryLogPathsStayInsideTemporaryWorkspace(t *testing.T) {
	workdir := t.TempDir()
	c, _ := recoveryController(t, workdir, recoveryConfig(), "wraith")
	path := c.Status().EventLogPath
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(workdir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("event log path %q escaped workspace %q", path, workdir)
	}
}
