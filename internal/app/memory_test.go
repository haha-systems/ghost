package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/memory"
	"github.com/haha-systems/ghost/internal/run"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func TestMemoryInspectorGolden(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m.colorProfile = colorprofile.ASCII
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if err := m.chooseAgent("wraith"); err != nil {
		t.Fatal(err)
	}
	m.config.Memory.Enabled = true
	m.projection.MemoryByAgent = map[core.AgentID]run.MemoryHistory{m.selectedAgent().ID: mixedMemoryHistory(t)}
	m, _ = sendCommand(t, m, ":memory")
	view := m.View().Content
	if !strings.Contains(view, "MEMORY") || !strings.Contains(view, "verified-with-evidence") {
		t.Fatalf(":memory did not show the memory inspector (size=%dx%d screen=%d state=%#v):\n%s", m.width, m.height, m.screen, m.conversation, view)
	}
	golden.RequireEqual(t, trimViewPadding(view))
}

func TestMemoryInspectorFitsSmallTerminalAndShowsOffState(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m.colorProfile = colorprofile.ASCII
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = sendCommand(t, m, ":memory")
	view := m.View().Content
	if !strings.Contains(view, "Memory off (not configured)") {
		t.Fatalf("memory-off state is missing:\n%s", view)
	}
	for lineNo, line := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(line); width > 80 {
			t.Fatalf("line %d is %d cells wide:\n%s", lineNo+1, width, view)
		}
	}
}

func TestMemoryInspectorShowsUnavailableReason(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.config.Memory.Enabled = true
	m.projection.Memory.Events = []core.Event{memoryEvent(t, core.MemoryEventUnavailable, map[string]string{"reason": "workspace_space_missing"})}
	m, _ = sendCommand(t, m, ":memory")
	if !strings.Contains(m.View().Content, "Memory unavailable: workspace_space_missing") {
		t.Fatalf("memory availability reason is missing:\n%s", m.View().Content)
	}
}

func TestMemoryInspectorShowsRetrievalFailure(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.config.Memory.Enabled = true
	m.projection.Memory.Events = []core.Event{memoryEvent(t, core.MemoryEventRetrievalFailed, map[string]string{"error_class": "timeout"})}
	m, _ = sendCommand(t, m, ":memory")
	if !strings.Contains(m.View().Content, "Memory retrieval failed: timeout") {
		t.Fatalf("memory retrieval failure is missing:\n%s", m.View().Content)
	}
}

func TestMemoryInspectorOpensFromPalette(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(t, m, control('p'))
	for _, r := range "memory" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	m, _ = updateModel(t, m, press("enter", tea.KeyEnter))
	if !strings.Contains(m.View().Content, "Memory off (not configured)") {
		t.Fatalf("palette did not open the memory inspector:\n%s", m.View().Content)
	}
}

func TestSelectingMemoryShowsFullProvenance(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m.colorProfile = colorprofile.ASCII
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if err := m.chooseAgent("wraith"); err != nil {
		t.Fatal(err)
	}
	m.projection.MemoryByAgent = map[core.AgentID]run.MemoryHistory{m.selectedAgent().ID: mixedMemoryHistory(t)}
	m, _ = sendCommand(t, m, ":memory")
	m, _ = updateModel(t, m, press("down", tea.KeyDown))
	m, _ = updateModel(t, m, press("enter", tea.KeyEnter))
	view := m.View().Content
	for _, want := range []string{
		"missing-evidence", "Scope: task", "Agent: AGENT-WRAITH", "Task: task-review",
		"Claim: inference", "Validation: unverified", "Confidence: 0.40", "Evidence: MISSING",
		"Source references:", "Counterevidence: none", "Backend state:", "Foreign: no",
		"Conflicts: none reported", "Superseded: no", "Text: The task result has no source reference.",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("memory detail does not show %q:\n%s", want, view)
		}
	}
}

func TestMemoryDetailScrollsOnSmallTerminal(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m.colorProfile = colorprofile.ASCII
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if err := m.chooseAgent("wraith"); err != nil {
		t.Fatal(err)
	}
	m.config.Memory.Enabled = true
	m.projection.MemoryByAgent = map[core.AgentID]run.MemoryHistory{m.selectedAgent().ID: mixedMemoryHistory(t)}
	m, _ = sendCommand(t, m, ":memory")
	m, _ = updateModel(t, m, press("down", tea.KeyDown))
	m, _ = updateModel(t, m, press("enter", tea.KeyEnter))
	m, _ = updateModel(t, m, press("pgdown", tea.KeyPgDown))
	view := m.View().Content
	if !strings.Contains(view, "Text: The task result has no source reference.") {
		t.Fatalf("memory details could not scroll to the record text:\n%s", view)
	}
	for lineNo, line := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(line); width > 80 {
			t.Fatalf("line %d is %d cells wide:\n%s", lineNo+1, width, view)
		}
	}
}

func TestMemoryCommandsDispatchThroughController(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	service := &appMemoryService{result: core.MemoryRecallResult{
		Records: []core.MemoryRecord{{
			ID: "explicit-note", Scope: core.MemoryScopeWorkspace, Text: "explicit recall result",
			Provenance: core.MemoryProvenance{ClaimKind: core.ClaimObservation, Validation: core.ValidationUnverified, MissingEvidence: true},
		}},
		Context: "explicit recall result", Tokens: 5,
	}}
	installAppMemoryService(t, &m, service)
	if err := m.chooseAgent("veil"); err != nil {
		t.Fatal(err)
	}
	before := len(m.controller.AllMessages())
	m, _ = sendCommand(t, m, ":recall explicit recall result")
	if service.request.Query != "explicit recall result" || service.request.Requester != m.selectedAgent().ID || service.request.Reason != "operator_recall" {
		t.Fatalf("recall request = %#v", service.request)
	}
	if len(m.controller.AllMessages()) != before || !strings.Contains(m.View().Content, "explicit-note") {
		t.Fatalf("recall sent a message or did not show its package:\n%s", m.View().Content)
	}
	m, _ = sendCommand(t, m, ":remember Keep the workspace review path")
	if len(service.writes) != 1 {
		t.Fatalf("writes = %#v, want one queued memory", service.writes)
	}
	write := service.writes[0]
	if write.Scope != core.MemoryScopeWorkspace || write.Provenance.ClaimKind != core.ClaimInstruction || write.Provenance.Agent != m.selectedAgent().ID || write.Text != "Keep the workspace review path" {
		t.Fatalf("human memory write = %#v", write)
	}
	if !strings.Contains(m.View().Content, "queued") || len(m.controller.AllMessages()) != before {
		t.Fatalf("remember did not show queued state or changed messages:\n%s", m.View().Content)
	}
}

func TestMemoryInspectorUsesSelectedTaskHistory(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	task := core.Task{ID: "task_00000000000000010000000000000001", Owner: m.projection.Agents[0].ID}
	m.projection.Tasks = []core.Task{task}
	m.conversation.rows = []conversationRow{{kind: rowTask, task: &task}}
	m.conversation.selected["all"] = 0
	m.projection.MemoryByTask = map[core.TaskID]run.MemoryHistory{task.ID: {
		Events: []core.Event{memoryEvent(t, core.MemoryEventRetrievalCompleted, map[string]any{"records": []core.MemoryRecord{{ID: "task-only", Scope: core.MemoryScopeTask, Text: "task memory"}}})},
	}}
	m, _ = sendCommand(t, m, ":memory")
	view := m.View().Content
	if !strings.Contains(view, "scope=task") || !strings.Contains(view, "task-only") {
		t.Fatalf("selected task memory is missing:\n%s", view)
	}
}

func TestWorkspaceMemoryInspectorFiltersPrivateRecords(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.config.Memory.Enabled = true
	history := mixedMemoryHistory(t)
	m.projection.Memory = history
	m, _ = sendCommand(t, m, ":memory")
	view := m.View().Content
	if !strings.Contains(view, "verified-with-evidence") || strings.Contains(view, "missing-evidence") || strings.Contains(view, "superseded-record") || strings.Contains(view, "failed candidate") {
		t.Fatalf("workspace inspector did not filter task and agent memory:\n%s", view)
	}
}

func TestMemoryInspectorShowsMetadataWhenContentWasNotPersisted(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.config.Memory.Enabled = true
	m.projection.Memory = run.MemoryHistory{Events: []core.Event{
		memoryEvent(t, core.MemoryEventRetrievalCompleted, map[string]any{
			"returned": 1, "record_ids": []string{"metadata-only"},
			"record_metadata": []map[string]any{{
				"id": "metadata-only", "scope": "workspace", "claim_kind": "inference",
				"source_agent": "agent-wraith", "content_bytes": 42, "score": 0.8,
			}},
		}),
		memoryEvent(t, core.MemoryEventContextPrepared, map[string]any{
			"memory_ids": []string{"metadata-only"}, "tokens": 12,
		}),
	}}
	m, _ = sendCommand(t, m, ":memory")
	view := m.View().Content
	for _, want := range []string{"metadata-only", "42 bytes", "content not persisted", "prepared"} {
		if !strings.Contains(strings.ToLower(view), want) {
			t.Errorf("memory inspector is missing %q:\n%s", want, view)
		}
	}
}

type appMemoryService struct {
	request core.MemoryRecallRequest
	result  core.MemoryRecallResult
	writes  []core.MemoryWrite
}

func (s *appMemoryService) Recall(_ context.Context, request core.MemoryRecallRequest) core.MemoryRecallResult {
	s.request = request
	return s.result
}

func (s *appMemoryService) ContextDelivered(core.MemoryRecallRequest, core.MemoryRecallResult) {}

func (s *appMemoryService) Enqueue(write core.MemoryWrite) bool {
	s.writes = append(s.writes, write)
	return true
}

func (s *appMemoryService) Close(context.Context) (memory.CloseResult, error) {
	return memory.CloseResult{}, nil
}

func installAppMemoryService(t *testing.T, m *Model, service run.MemoryService) {
	t.Helper()
	m.config.Memory.Enabled = true
	_ = m.controller.Close()
	controller := run.New(m.config, nil, run.Options{Runtime: fake.NewRuntime(), Memory: service})
	m.controller = controller
	m.runEvents, _ = controller.Subscribe()
	if err := controller.SetSessions(m.sessions); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	m.refreshProjection()
}

func mixedMemoryHistory(t *testing.T) run.MemoryHistory {
	t.Helper()
	base := core.MemoryProvenance{Agent: "agent-wraith", Task: "task-review", Confidence: 0.4, Validation: core.ValidationUnverified}
	records := []core.MemoryRecord{
		{ID: "verified-with-evidence", Scope: core.MemoryScopeWorkspace, Text: "Review passed with a source artifact.", Provenance: core.MemoryProvenance{ClaimKind: core.ClaimVerifiedResult, Validation: core.ValidationVerified, Confidence: 0.9, Evidence: []string{"artifact://review/report"}}},
		{ID: "missing-evidence", Scope: core.MemoryScopeTask, Text: "The task result has no source reference.", Provenance: core.MemoryProvenance{ClaimKind: core.ClaimInference, Validation: core.ValidationUnverified, Confidence: 0.4, MissingEvidence: true}},
		{ID: "foreign-record", Scope: core.MemoryScopeWorkspace, Text: "A record without Ghost provenance.", Foreign: true, BackendState: "accepted", ReportedConfidence: 0.8, Provenance: core.MemoryProvenance{ClaimKind: core.ClaimObservation, Validation: core.ValidationUnverified, Confidence: 0.8, MissingEvidence: true}},
		{ID: "disputed-left", Scope: core.MemoryScopeWorkspace, Text: "The cache uses path A.", ConflictIDs: []string{"disputed-right"}, Provenance: core.MemoryProvenance{ClaimKind: core.ClaimInference, Validation: core.ValidationDisputed, Confidence: 0.5, Evidence: []string{"event://trace-a"}, Counterevidence: []string{"event://trace-b"}}},
		{ID: "disputed-right", Scope: core.MemoryScopeWorkspace, Text: "The cache uses path B.", ConflictIDs: []string{"disputed-left"}, Provenance: core.MemoryProvenance{ClaimKind: core.ClaimInference, Validation: core.ValidationDisputed, Confidence: 0.4, Evidence: []string{"event://trace-b"}, Challenges: "disputed-left"}},
		{ID: "superseded-record", Scope: core.MemoryScopeAgent, Text: "An older workspace finding.", BackendState: "deprecated", Provenance: core.MemoryProvenance{ClaimKind: core.ClaimObservation, Validation: core.ValidationSuperseded, Confidence: 0.3, Evidence: []string{"event://older"}}},
	}
	for i := range records {
		if records[i].Provenance.Agent == "" {
			records[i].Provenance.Agent = base.Agent
		}
		if records[i].Provenance.Task == "" {
			records[i].Provenance.Task = base.Task
		}
	}
	encoded, err := json.Marshal(map[string]any{"records": records, "returned": len(records), "latency_ms": 4, "record_ids": recordIDs(records)})
	if err != nil {
		t.Fatal(err)
	}
	events := []core.Event{
		memoryEvent(t, core.MemoryEventRetrievalStarted, map[string]any{"reason": "task_started"}),
		memoryEvent(t, core.MemoryEventRetrievalCompleted, json.RawMessage(encoded)),
		memoryEvent(t, core.MemoryEventContextInjected, map[string]any{"memory_ids": []string{"verified-with-evidence", "disputed-left"}, "tokens": 38, "truncated": true}),
		memoryEvent(t, core.MemoryEventWriteQueued, map[string]any{"write_id": "wq-1", "summary": "queued candidate", "scope": "workspace", "claim_kind": "inference"}),
		memoryEvent(t, core.MemoryEventWriteStored, map[string]any{"write_id": "ws-1", "record_id": "stored-record", "summary": "stored candidate", "scope": "workspace", "claim_kind": "inference"}),
		memoryEvent(t, core.MemoryEventWriteFailed, map[string]any{"write_id": "wf-1", "summary": "failed candidate", "reason": "backend timeout", "scope": "task", "claim_kind": "inference"}),
		memoryEvent(t, core.MemoryEventWriteDropped, map[string]any{"write_id": "wd-1", "summary": "dropped candidate", "reason": "queue_full", "scope": "workspace", "claim_kind": "inference"}),
		memoryEvent(t, core.MemoryEventWriteSkipped, map[string]any{"write_id": "wk-1", "summary": "skipped candidate", "reason": "no agent space configured", "scope": "agent", "claim_kind": "observation"}),
		memoryEvent(t, core.MemoryEventWriteLost, map[string]any{"write_id": "wl-1", "summary": "lost candidate", "reason": "ghost restarted before write completed", "scope": "task", "claim_kind": "observation"}),
		memoryEvent(t, core.MemoryEventScopeFiltered, map[string]any{"count": 1}),
		memoryEvent(t, core.MemoryEventChallengeRecorded, map[string]any{"record_id": "disputed-right"}),
		memoryEvent(t, core.MemoryEventSinkDropped, map[string]any{"count": 2}),
	}
	return run.MemoryHistory{Counters: run.MemoryCounters{
		Retrievals: 1, RetrievalLatencyMS: 4, InjectedTokens: 38, Failures: 1,
		WritesQueued: 1, WritesStored: 1, WritesFailed: 1, WritesDropped: 1,
		WritesSkipped: 1, WritesLost: 1, ScopeFiltered: 1, Challenges: 1, SinkEventsDropped: 2,
	}, Events: events}
}

func memoryEvent(t *testing.T, eventType string, payload any) core.Event {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if raw, ok := payload.(json.RawMessage); ok {
		encoded, err = raw, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{ID: core.EventID(eventType + "-" + time.Now().Format("150405.000000000")), Time: time.Now().UTC(), Type: eventType, Category: core.CategoryMemory, Payload: encoded, Source: "memory"}
}

func recordIDs(records []core.MemoryRecord) []string {
	ids := make([]string, 0, len(records))
	for _, item := range records {
		ids = append(ids, item.ID)
	}
	return ids
}

func trimViewPadding(view string) string {
	lines := strings.Split(view, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}
