package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/run"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func TestMainViewUsesConversationLayout(t *testing.T) {
	m := New(config.Default(), nil)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View().Content
	for _, want := range []string{"GHOST / BLOODWIRE", "0 ALL", "SHARED CONVERSATION", "> @all"} {
		if !strings.Contains(view, want) {
			t.Errorf("main view does not contain %q:\n%s", want, view)
		}
	}
}

func TestConversationKeyboardAndComposerFocus(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m, _ = updateModel(t, m, press("1", '1'))
	if m.conversation.tab != 1 || m.selectedAgentName() != "veil" {
		t.Fatalf("selected tab = %d (%q), want veil", m.conversation.tab, m.selectedAgentName())
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	for _, r := range "review1" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	if m.conversation.tab != 1 || m.conversation.composer != "review1" {
		t.Fatalf("typing changed tab or input: tab=%d input=%q", m.conversation.tab, m.conversation.composer)
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	if m.conversation.focus != focusConversation {
		t.Fatalf("focus after Tab = %v, want conversation", m.conversation.focus)
	}
	m, _ = updateModel(t, m, control('e'))
	if !m.conversation.eventsOpen || m.conversation.focus != focusEvents {
		t.Fatal("Ctrl+E did not open and focus events")
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	if m.conversation.focus != focusComposer {
		t.Fatal("Tab did not cycle from events to composer")
	}
	m, _ = updateModel(t, m, press("esc", tea.KeyEscape))
	if m.conversation.eventsOpen || m.conversation.focus != focusConversation {
		t.Fatal("Esc did not close events and return focus")
	}
	_, cmd := updateModel(t, m, control('c'))
	if cmd == nil {
		t.Fatal("Ctrl+C did not request quit")
	}
}

func TestGlobalShortcutsWorkWithEventsFocused(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m, _ = updateModel(t, m, control('e'))
	m, _ = updateModel(t, m, press("2", '2'))
	if m.selectedAgentName() != "wraith" || m.conversation.focus != focusEvents {
		t.Fatal("number shortcut did not select an agent while events had focus")
	}
	m, _ = updateModel(t, m, press("c", 'c'))
	if m.controller.Status().Requested != run.CESPolicyOn {
		t.Fatal("CES shortcut did not reach the controller while events had focus")
	}
}

func TestComposerUsesTabDefaultsAndMentionOverrides(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	sendText(t, &m, "broadcast everyone")
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	m, _ = updateModel(t, m, press("2", '2'))
	if got := m.defaultRecipient(); got != "@wraith" {
		t.Fatalf("default recipient = %q, want @wraith", got)
	}
	sendText(t, &m, "default to wraith")
	sendText(t, &m, "@veil please review")
	sendText(t, &m, "@wraith @veil compare these")
	sendText(t, &m, "@all broadcast override")

	messages := m.controller.AllMessages()
	if len(messages) != 5 {
		t.Fatalf("messages = %d, want 5", len(messages))
	}
	assertRecipients(t, m, messages[0], "veil", "wraith")
	assertRecipients(t, m, messages[1], "wraith")
	assertRecipients(t, m, messages[2], "veil")
	assertRecipients(t, m, messages[3], "wraith", "veil")
	assertRecipients(t, m, messages[4], "veil", "wraith")
}

func TestAgentTabShowsItsMessagesAndBroadcasts(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	for _, send := range []struct {
		recipients []string
		text       string
	}{
		{[]string{"veil"}, "veil targeted"},
		{[]string{"wraith"}, "wraith targeted"},
		{[]string{"all"}, "shared broadcast"},
	} {
		if err := m.controller.Send(send.recipients, send.text); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.controller.Observe(runtime.Event{AgentID: "veil", SessionID: "veil", TurnID: "turn-2", Kind: runtime.KindMessage, Summary: "veil reply", Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(m.controller.AllMessages()) == 4 })
	m.refreshProjection()
	if err := m.chooseAgent("veil"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"veil targeted", "shared broadcast", "veil reply"} {
		if !conversationHasMessage(m.conversation.rows, want) {
			t.Errorf("veil tab is missing %q", want)
		}
	}
	if conversationHasMessage(m.conversation.rows, "wraith targeted") {
		t.Fatal("veil tab includes a message addressed only to wraith")
	}
	if err := m.chooseAgent("wraith"); err != nil {
		t.Fatal(err)
	}
	if !conversationHasMessage(m.conversation.rows, "wraith targeted") || !conversationHasMessage(m.conversation.rows, "shared broadcast") {
		t.Fatal("wraith tab is missing its targeted message or broadcast")
	}
	if conversationHasMessage(m.conversation.rows, "veil reply") {
		t.Fatal("wraith tab includes a message sent only by veil")
	}
}

func TestRecoveredHistoryRendersInAllAndAgentTabs(t *testing.T) {
	workdir := t.TempDir()
	agents := config.AgentSet{
		"veil":   {Runtime: "codex", WorkingDir: workdir},
		"wraith": {Runtime: "codex", WorkingDir: workdir},
	}
	cfg := config.Default()
	cfg.Agents = &agents
	cfg.AgentOrder = []string{"veil", "wraith"}
	first := run.New(cfg, nil, run.Options{Runtime: fake.NewRuntime(), WorkDir: workdir})
	session := fake.NewSession("veil")
	if err := first.SetSessions(map[string]runtime.Session{"veil": session}); err != nil {
		t.Fatal(err)
	}
	if err := first.Send([]string{"veil"}, "recovered message"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		messages := first.AllMessages()
		return len(messages) == 1 && messages[0].Delivery == core.DeliveryDelivered
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	corruptPath := filepath.Join(workdir, ".ghost", "events", "000-corrupt.jsonl")
	if err := os.WriteFile(corruptPath, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewWithWorkDir(cfg, nil, workdir)
	t.Cleanup(func() { _ = m.controller.Close() })
	if !conversationHasMessage(m.conversation.rows, "recovered message") {
		t.Fatal("ALL tab is missing the recovered message")
	}
	if err := m.chooseAgent("veil"); err != nil {
		t.Fatal(err)
	}
	if !conversationHasMessage(m.conversation.rows, "recovered message") {
		t.Fatal("VEIL tab is missing the recovered message")
	}
	var task *core.Task
	for i := range m.projection.Tasks {
		if m.projection.Tasks[i].FailureReason == "interrupted: ghost restarted" {
			task = &m.projection.Tasks[i]
		}
	}
	if task == nil {
		t.Fatal("VEIL tab did not restore the interrupted task")
	}
	foundTaskLabel := false
	for _, row := range m.conversation.rows {
		if row.task != nil && row.task.ID == task.ID {
			foundTaskLabel = strings.Contains(strings.Join(m.rowLines(row, 180), " "), "interrupted: ghost restarted")
		}
	}
	if !foundTaskLabel {
		t.Fatal("interrupted task reason is not visible in the conversation")
	}

	m.chooseTab(0)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	if !strings.Contains(m.View().Content, "recovered 1 message, 1 task") {
		t.Fatal("TUI header does not show the recovered message and task counts")
	}
	events := strings.Join(m.eventLines(40, 240, false), "\n")
	for _, want := range []string{"workspace.recovered", "1 message", "1 task", "workspace.recovery_warning", "000-corrupt.jsonl"} {
		if !strings.Contains(events, want) {
			t.Errorf("recovery event panel is missing %q:\n%s", want, events)
		}
	}
}

func TestConversationSummarizesSignificantStructuredEvents(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	veil, ok := m.agentByName("veil")
	if !ok {
		t.Fatal("missing veil fixture agent")
	}
	base := time.Unix(100, 0)
	add := func(seq uint64, eventType string, category core.Category, payload string) {
		m.applyCoreEvent(core.Event{
			ID: core.EventID(fmt.Sprintf("event_%d", seq)), Time: base.Add(time.Duration(seq) * time.Second),
			Actor: core.Actor{Kind: core.ActorAgent, ID: string(veil.ID)}, AgentID: veil.ID,
			Type: eventType, Category: category, Seq: seq, Payload: []byte(payload),
		})
	}
	add(1, "tool.call", core.CategoryTool, `{"tool_name":"read"}`)
	add(2, "tool.call", core.CategoryTool, `{"tool_name":"search"}`)
	add(3, "tool.result", core.CategoryTool, `{"result":"routine detail"}`)
	add(4, "decision.qac", core.CategoryDecision, `{"from":"entry","to":"review","action":"select resource"}`)
	add(5, "ces.phase", core.CategoryCoordination, `{"phase":"verification"}`)
	add(6, "provider.error", core.CategoryCoordination, `{"error":"request failed"}`)
	for _, want := range []string{"VEIL ran 2 tools", "QAC decision · entry → review · select resource", "CES phase · VERIFICATION", "! VEIL · request failed"} {
		if !conversationHasRowText(m.conversation.rows, want) {
			t.Errorf("conversation is missing summary %q", want)
		}
	}
	if conversationHasRowText(m.conversation.rows, "routine detail") {
		t.Fatal("low-level tool result appeared in the conversation")
	}
}

func TestConversationRendersRoutingRejection(t *testing.T) {
	m, _ := conversationModel(t, "wraith")
	wraith, ok := m.agentByName("wraith")
	if !ok {
		t.Fatal("missing wraith fixture agent")
	}
	m.applyCoreEvent(core.Event{
		ID: "event_routing_rejection", Time: time.Unix(200, 0), Seq: 1,
		Actor: core.Actor{Kind: core.ActorAgent, ID: string(wraith.ID)}, AgentID: wraith.ID,
		TaskID: "task_routing_rejection", Type: "coordination.routing_rejected", Category: core.CategoryCoordination,
		Payload: []byte(`{"sender":"` + string(wraith.ID) + `","action":"delegate","recipient":"VEIL","reason":"delegations are disabled","task_id":"task_routing_rejection"}`),
	})

	want := "routing rejected: WRAITH delegate -> VEIL (task task_routing_rejection): delegations are disabled"
	if !conversationHasRowText(m.conversation.rows, want) {
		t.Fatalf("conversation does not show routing rejection %q", want)
	}
	if got := strings.Join(m.eventLines(10, 220, false), "\n"); !strings.Contains(got, want) {
		t.Fatalf("event stream does not show routing rejection %q:\n%s", want, got)
	}
}

func TestAgentActivityAndPerTabScrollState(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	veil, _ := m.agentByName("veil")
	for i := 0; i < 20; i++ {
		m.projection.Messages = append(m.projection.Messages, core.Message{
			ID: core.MessageID(fmt.Sprintf("message_%02d", i)), Sender: core.HumanAgentID,
			Recipients: []core.AgentID{veil.ID}, Type: core.MessageDirect,
			Content: fmt.Sprintf("message %02d", i), CreatedAt: time.Unix(int64(i+1), 0),
		})
	}
	m.conversation.rowsDirty = true
	m.rebuildConversationRows()
	if err := m.chooseAgent("veil"); err != nil {
		t.Fatal(err)
	}
	for i := range m.projection.Agents {
		if m.projection.Agents[i].ID == veil.ID {
			m.projection.Agents[i].Activity = "reviewing runtime changes"
		}
	}
	if !strings.Contains(m.View().Content, "ACTIVITY · reviewing runtime changes") {
		t.Fatal("agent tab did not show current activity")
	}
	m.conversation.scroll[m.tabKey()] = 3
	m.conversation.selected[m.tabKey()] = 4
	m.chooseTab(0)
	m.conversation.scroll[m.tabKey()] = 8
	m.conversation.selected[m.tabKey()] = 10
	m.chooseAgent("veil")
	if m.conversation.scroll[m.tabKey()] != 3 || m.conversation.selected[m.tabKey()] != 4 {
		t.Fatal("switching tabs did not preserve the agent scroll and selection")
	}
	m.chooseTab(0)
	if m.conversation.scroll[m.tabKey()] != 8 || m.conversation.selected[m.tabKey()] != 10 {
		t.Fatal("switching tabs did not preserve the shared scroll and selection")
	}
}

func conversationHasMessage(rows []conversationRow, content string) bool {
	for _, row := range rows {
		if row.message != nil && row.message.Content == content {
			return true
		}
	}
	return false
}

func conversationHasRowText(rows []conversationRow, content string) bool {
	for _, row := range rows {
		if row.text == content {
			return true
		}
	}
	return false
}

func TestEventsPanelFiltersSearchesFollowsAndExpands(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	addProjectedEvent(t, &m, "veil", event.KindError, "resource choice")
	addProjectedEvent(t, &m, "wraith", event.KindError, "provider failed")
	if err := m.chooseAgent("veil"); err != nil {
		t.Fatal(err)
	}
	m, _ = updateModel(t, m, control('e'))
	if len(m.conversation.filteredEvents) != 2 {
		t.Fatalf("agent-filtered events = %d, want the capability brief and provider error", len(m.conversation.filteredEvents))
	}
	m, _ = updateModel(t, m, press("a", 'a'))
	if len(m.conversation.filteredEvents) != 4 {
		t.Fatalf("events after disabling agent filter = %d, want both capability briefs and both provider errors", len(m.conversation.filteredEvents))
	}
	m, _ = updateModel(t, m, press("k", 'k'))
	if len(m.conversation.filteredEvents) != 0 {
		t.Fatalf("conversation category retained %d decision/coordination events", len(m.conversation.filteredEvents))
	}
	m.conversation.eventCategory = 0
	m.refreshFilteredEvents()
	m, _ = updateModel(t, m, press("/", '/'))
	for _, r := range "resource choice" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	if len(m.conversation.filteredEvents) != 1 || !strings.Contains(m.conversation.filteredEvents[0].Type, "provider.error") {
		t.Fatalf("search matches = %#v, want one provider error", m.conversation.filteredEvents)
	}
	m, _ = updateModel(t, m, press("enter", tea.KeyEnter))
	if m.conversation.searchMode {
		t.Fatal("Enter did not finish event search")
	}
	m, _ = updateModel(t, m, press("enter", tea.KeyEnter))
	if m.conversation.expandedEvent == "" || !strings.Contains(m.View().Content, "resource choice") {
		t.Fatal("Enter did not expand the selected raw event")
	}
	m, _ = sendCommand(t, m, ":events")
	if m.conversation.eventsOpen {
		t.Fatal(":events did not close the panel")
	}
	m, _ = sendCommand(t, m, ":events")
	if !m.conversation.eventsOpen {
		t.Fatal(":events did not open the panel")
	}
}

func TestEventFollowPauseKeepsSelection(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	addProjectedEvent(t, &m, "veil", event.KindError, "first")
	addProjectedEvent(t, &m, "veil", event.KindError, "second")
	m, _ = updateModel(t, m, control('e'))
	m, _ = updateModel(t, m, press("f", 'f'))
	selected := m.conversation.eventSelected
	addProjectedEvent(t, &m, "veil", event.KindError, "third")
	m.refreshProjection()
	if m.conversation.eventFollow || m.conversation.eventSelected != selected {
		t.Fatalf("paused selection moved: follow=%v selected=%d want %d", m.conversation.eventFollow, m.conversation.eventSelected, selected)
	}
	m, _ = updateModel(t, m, press("f", 'f'))
	if !m.conversation.eventFollow || m.conversation.eventSelected != len(m.conversation.filteredEvents)-1 {
		t.Fatal("follow did not return to the newest event")
	}
}

func TestTaskDetailOpensFromConversationSelection(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	sendText(t, &m, "review runtime changes")
	if len(m.conversation.rows) < 2 {
		t.Fatalf("conversation rows = %d, want task and message", len(m.conversation.rows))
	}
	taskIndex := -1
	for i, row := range m.conversation.rows {
		if row.task != nil {
			taskIndex = i
			break
		}
	}
	if taskIndex < 0 {
		t.Fatal("controller projection has no task row")
	}
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	selected := m.conversation.selected[m.tabKey()]
	for selected > taskIndex {
		m, _ = updateModel(t, m, press("up", tea.KeyUp))
		selected--
	}
	for selected < taskIndex {
		m, _ = updateModel(t, m, press("down", tea.KeyDown))
		selected++
	}
	m, _ = updateModel(t, m, press("enter", tea.KeyEnter))
	if m.conversation.taskDetail == "" {
		t.Fatalf("Enter did not open task row %d from selected %d: %#v", taskIndex, m.conversation.selected[m.tabKey()], m.conversation.rows)
	}
	view := m.View().Content
	for _, want := range []string{"TASK DETAIL", "Owner:", "Participants:", "Status:", "CES policy:", "Related messages:"} {
		if !strings.Contains(view, want) {
			t.Errorf("task detail does not contain %q", want)
		}
	}
	m, _ = updateModel(t, m, press("esc", tea.KeyEscape))
	if m.conversation.taskDetail != "" {
		t.Fatal("Esc did not return from task detail")
	}
}

func TestDelegationAndResultCardsRenderForBothAgents(t *testing.T) {
	m, _ := conversationModel(t, "wraith", "veil")
	m.colorProfile = colorprofile.ASCII
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 110, Height: 34})
	wraith, _ := m.agentByName("wraith")
	veil, _ := m.agentByName("veil")
	now := time.Now().UTC()
	finished := now.Add(time.Millisecond)
	parentID, childID := core.TaskID("task_00000000000000000000000000000001"), core.TaskID("task_00000000000000000000000000000002")
	threadID := core.ThreadID("thread_00000000000000000000000000000001")
	parent := core.Task{ID: parentID, Requester: core.HumanAgentID, Owner: wraith.ID, Participants: []core.AgentID{wraith.ID}, Status: core.TaskRunning, CreatedAt: now, UpdatedAt: now, StartedAt: &now, CESPolicy: core.CESOff, ThreadID: threadID, Objective: "Review runtime routing"}
	child := core.Task{ID: childID, Requester: wraith.ID, Owner: veil.ID, Parent: &parentID, Status: core.TaskCompleted, CreatedAt: now, UpdatedAt: finished, StartedAt: &now, FinishedAt: &finished, CESPolicy: parent.CESPolicy, ThreadID: parent.ThreadID, Depth: 1, Objective: "Review the event adapter", ExpectedDeliverable: "Findings and one risk"}
	delegation := core.Message{ID: "message_00000000000000000000000000000001", Sender: wraith.ID, Recipients: []core.AgentID{veil.ID}, ThreadID: parent.ThreadID, TaskID: &childID, Type: core.MessageTaskDelegation, Content: child.Objective, CreatedAt: now, Delivery: core.DeliveryDelivered, RecipientDelivery: map[core.AgentID]core.DeliveryStatus{veil.ID: core.DeliveryDelivered}}
	result := core.Message{ID: "message_00000000000000000000000000000002", Sender: veil.ID, Recipients: []core.AgentID{wraith.ID}, ThreadID: parent.ThreadID, TaskID: &childID, Type: core.MessageTaskResult, Content: "The adapter preserves event order.", CreatedAt: finished, Delivery: core.DeliveryDelivered, RecipientDelivery: map[core.AgentID]core.DeliveryStatus{wraith.ID: core.DeliveryDelivered}}
	m.projection.Tasks = []core.Task{parent, child}
	m.projection.Messages = []core.Message{delegation, result}
	m.conversation.rowsDirty = true
	m.rebuildConversationRows()
	view := m.View().Content
	for _, want := range []string{"WRAITH → @veil", "DELEGATION", "RESULT", "↳ parent " + shortTaskID(string(parentID)), "delivered", "The adapter preserves event order."} {
		if !strings.Contains(view, want) {
			t.Errorf("conversation view does not show %q:\n%s", want, view)
		}
	}
	golden.RequireEqual(t, view)
	for _, name := range []string{"wraith", "veil"} {
		if err := m.chooseAgent(name); err != nil {
			t.Fatal(err)
		}
		if !conversationHasMessage(m.conversation.rows, child.Objective) || !conversationHasMessage(m.conversation.rows, result.Content) {
			t.Errorf("%s tab does not show delegation and result", name)
		}
	}
	m.conversation.taskDetail = parent.ID
	view = m.View().Content
	if !strings.Contains(view, "Children: "+string(child.ID)) || !strings.Contains(view, "↳ "+shortTaskID(string(child.ID))) {
		t.Fatalf("parent detail does not show child and related messages:\n%s", view)
	}
}

func TestCancelCommandPrefixAndTaskDetailKey(t *testing.T) {
	m, sessions := conversationModel(t, "wraith", "veil")
	if err := m.controller.StartWithPolicy("cancel by prefix", run.CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	m.refreshProjection()
	first := m.projection.Tasks[0]
	m, _ = sendCommand(t, m, ":cancel "+string(first.ID[:16]))
	waitFor(t, func() bool {
		return m.controller.WorkspaceSnapshot().Tasks[0].Status == core.TaskCancelled
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && (sessions["wraith"].State() != runtime.StateIdle || sessions["veil"].State() != runtime.StateIdle) {
		time.Sleep(time.Millisecond)
	}
	if sessions["wraith"].State() != runtime.StateIdle || sessions["veil"].State() != runtime.StateIdle {
		t.Fatalf("cancel did not interrupt sessions: wraith=%s/%q veil=%s/%q", sessions["wraith"].State(), sessions["wraith"].LastInterruptedTurn(), sessions["veil"].State(), sessions["veil"].LastInterruptedTurn())
	}
	if err := m.controller.StartWithPolicy("cancel from detail", run.CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	m.refreshProjection()
	second := m.projection.Tasks[len(m.projection.Tasks)-1]
	m.conversation.taskDetail = second.ID
	m.conversation.focus = focusConversation
	m, _ = updateModel(t, m, press("x", 'x'))
	waitFor(t, func() bool {
		for _, task := range m.controller.WorkspaceSnapshot().Tasks {
			if task.ID == second.ID {
				return task.Status == core.TaskCancelled
			}
		}
		return false
	})
}

func TestCESControlsUseControllerStatus(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(t, m, press("c", 'c'))
	status := m.controller.Status()
	if status.Requested != run.CESPolicyOn || status.Effective != run.CESPolicyOff || !strings.Contains(status.PolicyReason, "QAC not configured") {
		t.Fatalf("controller status = %#v, want unsupported request", status)
	}
	if !strings.Contains(m.View().Content, "CES OFF — unsupported: QAC not configured") {
		t.Fatal("header does not show the effective unsupported CES state")
	}
	m, _ = sendCommand(t, m, ":ces off")
	if got := m.controller.Status().Requested; got != run.CESPolicyOff {
		t.Fatalf(":ces off requested %q, want off", got)
	}

	pending, sessions := qacPendingModel(t)
	pending, _ = updateModel(t, pending, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !pending.controller.Status().Pending {
		t.Fatal("test setup did not produce a pending policy")
	}
	if !strings.Contains(pending.View().Content, "CES OFF (pending)") {
		t.Fatal("header does not show the controller's pending CES state")
	}
	_ = sessions
}

func TestPaletteSelectsOverflowAgentAndLegacyReturns(t *testing.T) {
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("agent%02d", i)
	}
	m, _ := conversationModel(t, names...)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	if !strings.Contains(m.View().Content, "+3") {
		t.Fatal("agent overflow count is missing")
	}
	m, _ = updateModel(t, m, control('p'))
	for _, r := range "agent10" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	m, _ = updateModel(t, m, press("enter", tea.KeyEnter))
	if got := m.selectedAgentName(); got != "agent10" {
		t.Fatalf("palette agent = %q, want agent10", got)
	}
	m, _ = sendCommand(t, m, ":agent agent11")
	if got := m.selectedAgentName(); got != "agent11" {
		t.Fatalf(":agent selected %q, want agent11", got)
	}
	m, _ = sendCommand(t, m, ":legacy")
	if m.Screen() != LegacyDashboardScreen {
		t.Fatal(":legacy did not open the old dashboard")
	}
	m, _ = updateModel(t, m, press("esc", tea.KeyEscape))
	if m.Screen() != ConversationScreen {
		t.Fatal("Esc did not return from the old dashboard")
	}
}

func TestHelpAndQuitComposerCommands(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = sendCommand(t, m, ":help")
	if !m.conversation.helpOpen {
		t.Fatal(":help did not open key help")
	}
	if !strings.Contains(m.View().Content, ":memory :recall QUERY :remember TEXT") {
		t.Fatal("key help does not list the memory commands")
	}
	m, _ = sendCommand(t, m, ":help")
	if m.conversation.helpOpen {
		t.Fatal(":help did not close key help")
	}
	_, cmd := sendCommand(t, m, ":quit")
	if cmd == nil {
		t.Fatal(":quit did not request quit")
	}
}

func TestNewViewSendsAndCancelsThroughFakeRuntime(t *testing.T) {
	m, sessions := conversationModel(t, "veil")
	sendText(t, &m, "run the check")
	waitFor(t, func() bool { return sessions["veil"].State() == runtime.StateRunning })
	if len(m.controller.AllMessages()) != 1 {
		t.Fatal("new view did not send through the controller")
	}
	sendText(t, &m, "use the corrected session owner")
	waitFor(t, func() bool {
		return strings.Contains(sessions["veil"].LastSteeredText(), "use the corrected session owner")
	})
	m, _ = sendCommand(t, m, ":cancel")
	if len(m.controller.WorkspaceSnapshot().Tasks) == 0 || m.controller.WorkspaceSnapshot().Tasks[0].Status != core.TaskCancelled {
		t.Fatal("cancel command did not cancel the controller task")
	}
	waitFor(t, func() bool { return sessions["veil"].State() == runtime.StateIdle })
}

func TestFullAppWindowRenderShowsProjectionAndComposer(t *testing.T) {
	m, _ := conversationModel(t, "veil", "wraith")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	sendText(t, &m, "inspect session ownership")
	view := m.View().Content
	for _, want := range []string{"[0 ALL]", "SHARED CONVERSATION", "YOU → @all", "inspect session ownership", "> @all"} {
		if !strings.Contains(view, want) {
			t.Errorf("full app view does not contain %q", want)
		}
	}
}

func TestRenderLargeEventHistoryAndKeepFocusStable(t *testing.T) {
	m, _ := conversationModel(t, "veil")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	for i := 0; i < 5000; i++ {
		item := fakeCoreEvent(i)
		m.projection.Events = append(m.projection.Events, item)
		m.conversation.eventIDs[item.ID] = struct{}{}
	}
	m.conversation.rowsDirty = true
	m.rebuildConversationRows()
	m.refreshFilteredEvents()
	m, _ = updateModel(t, m, control('e'))
	m, _ = updateModel(t, m, press("f", 'f'))
	selected := m.conversation.eventSelected
	m.applyCoreEvent(fakeCoreEvent(5000))
	m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	for _, r := range "draft" {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	start := time.Now()
	view := m.View().Content
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("rendering 5000 events took %s", elapsed)
	}
	if !strings.Contains(view, "draft") || m.conversation.focus != focusComposer || m.conversation.eventSelected != selected {
		t.Fatal("large history changed composer focus, draft, or paused selection")
	}
}

func fakeCoreEvent(index int) core.Event {
	return core.Event{
		ID: core.EventID(fmt.Sprintf("event_%05d", index)), Time: time.Unix(int64(index+1), 0),
		Actor: core.Actor{Kind: core.ActorSystem, ID: "fixture"}, Source: "fixture",
		Type: "tool.call", Category: core.CategoryTool, Seq: uint64(index + 1), Payload: []byte(`{"tool_summary":"run"}`),
	}
}

func TestBloodwireGoldenViews(t *testing.T) {
	t.Setenv("TERM", "dumb")
	tests := []struct {
		name      string
		width     int
		height    int
		agents    []string
		configure func(*testing.T, *Model)
	}{
		{name: "all_80x24", width: 80, height: 24, agents: []string{"veil", "wraith"}, configure: seedConversation},
		{name: "all_100x30", width: 100, height: 30, agents: []string{"veil", "wraith"}, configure: seedConversation},
		{name: "all_140x40", width: 140, height: 40, agents: []string{"veil", "wraith"}, configure: seedConversation},
		{name: "agent_100x30", width: 100, height: 30, agents: []string{"veil", "wraith"}, configure: func(t *testing.T, m *Model) { seedConversation(t, m); _ = m.chooseAgent("veil") }},
		{name: "events_overlay_80x24", width: 80, height: 24, agents: []string{"veil", "wraith"}, configure: func(t *testing.T, m *Model) {
			seedConversation(t, m)
			addProjectedEvent(t, m, "veil", event.KindError, "resource plan")
			m.toggleEvents()
		}},
		{name: "events_side_140x40", width: 140, height: 40, agents: []string{"veil", "wraith"}, configure: func(t *testing.T, m *Model) {
			seedConversation(t, m)
			addProjectedEvent(t, m, "veil", event.KindError, "resource plan")
			m.toggleEvents()
		}},
		{name: "task_detail_100x30", width: 100, height: 30, agents: []string{"veil", "wraith"}, configure: func(t *testing.T, m *Model) {
			seedConversation(t, m)
			for i, row := range m.conversation.rows {
				if row.task != nil {
					m.conversation.selected[m.tabKey()] = i
					break
				}
			}
			m.conversation.taskDetail = m.conversation.rows[m.conversation.selected[m.tabKey()]].task.ID
		}},
		{name: "ces_pending_100x30", width: 100, height: 30, configure: func(t *testing.T, m *Model) { startPendingPolicy(t, m) }},
		{name: "overflow_140x40", width: 140, height: 40, agents: []string{"agent00", "agent01", "agent02", "agent03", "agent04", "agent05", "agent06", "agent07", "agent08", "agent09", "agent10", "agent11"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m Model
			if tc.name == "ces_pending_100x30" {
				m, _ = conversationModelFromConfig(t, appQACConfig())
			} else {
				m, _ = conversationModel(t, tc.agents...)
			}
			m.colorProfile = colorprofile.ASCII
			m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			if tc.configure != nil {
				tc.configure(t, &m)
			}
			golden.RequireEqual(t, m.View().Content)
		})
	}
}

func conversationModel(t *testing.T, names ...string) (Model, map[string]*fake.Session) {
	t.Helper()
	agents := config.AgentSet{}
	for _, name := range names {
		agents[name] = config.BackendConfig{Runtime: "codex", WorkingDir: "."}
	}
	cfg := config.Default()
	cfg.Agents = &agents
	m := New(cfg, nil)
	_ = m.controller.Close()
	rt := fake.NewRuntime()
	m.controller = run.New(cfg, nil, run.Options{Runtime: rt})
	m.runEvents, _ = m.controller.Subscribe()
	sessions := map[string]*fake.Session{}
	interfaces := map[string]runtime.Session{}
	for _, name := range names {
		session := fake.NewSession(name)
		sessions[name], interfaces[name] = session, session
	}
	m.sessions = interfaces
	if err := m.controller.SetSessions(interfaces); err != nil {
		t.Fatal(err)
	}
	m.workspaceName = "ghost"
	m.conversation = newConversationState()
	m.refreshProjection()
	controller := m.controller
	t.Cleanup(func() { _ = controller.Close() })
	return m, sessions
}

func sendText(t *testing.T, m *Model, text string) {
	t.Helper()
	if m.conversation.focus != focusComposer {
		*m, _ = updateModel(t, *m, press("tab", tea.KeyTab))
	}
	for _, r := range text {
		*m, _ = updateModel(t, *m, press(string(r), r))
	}
	*m, _ = updateModel(t, *m, press("enter", tea.KeyEnter))
}

func sendCommand(t *testing.T, m Model, command string) (Model, tea.Cmd) {
	if m.conversation.focus != focusComposer {
		m, _ = updateModel(t, m, press("tab", tea.KeyTab))
	}
	for _, r := range command {
		m, _ = updateModel(t, m, press(string(r), r))
	}
	return updateModel(t, m, press("enter", tea.KeyEnter))
}

func control(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: r, Mod: tea.ModCtrl})
}

func assertRecipients(t *testing.T, m Model, message core.Message, names ...string) {
	t.Helper()
	want := make(map[core.AgentID]bool, len(names))
	for _, name := range names {
		if name == "*" {
			want[core.BroadcastWildcard] = true
			continue
		}
		agent, ok := m.agentByName(name)
		if !ok {
			t.Fatalf("missing agent %q", name)
		}
		want[agent.ID] = true
	}
	got := make(map[core.AgentID]bool, len(message.Recipients))
	for _, id := range message.Recipients {
		got[id] = true
	}
	if len(got) != len(want) {
		t.Fatalf("recipients for %q = %v, want %v", message.Content, got, want)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("recipients for %q = %v, want %v", message.Content, got, want)
		}
	}
}

func addProjectedEvent(t *testing.T, m *Model, agent string, kind event.Kind, message string) {
	t.Helper()
	if err := m.controller.Record(agent, event.Event{Source: strings.ToUpper(agent), Kind: kind, Message: message}, nil); err != nil {
		t.Fatal(err)
	}
	m.refreshProjection()
}

func seedConversation(t *testing.T, m *Model) {
	t.Helper()
	if err := m.controller.Send([]string{"all"}, "Fix session ownership. Ask VEIL to review."); err != nil {
		t.Fatal(err)
	}
	// Deliveries run on per-recipient workers; wait for both before the
	// reply so the golden event order is deterministic.
	waitFor(t, func() bool {
		messages := m.controller.AllMessages()
		if len(messages) == 0 || len(messages[0].RecipientDelivery) < 2 {
			return false
		}
		for _, state := range messages[0].RecipientDelivery {
			if state != core.DeliveryDelivered && state != core.DeliveryRead {
				return false
			}
		}
		return true
	})
	if err := m.controller.Observe(runtime.Event{AgentID: "veil", SessionID: "veil", TurnID: "turn-1", Kind: runtime.KindMessage, Summary: "Applying the correction now.", Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(m.controller.AllMessages()) == 2 })
	m.refreshProjection()
}

func qacPendingModel(t *testing.T) (Model, map[string]*fake.Session) {
	t.Helper()
	m, sessions := conversationModelFromConfig(t, appQACConfig())
	startPendingPolicy(t, &m)
	return m, sessions
}

func startPendingPolicy(t *testing.T, m *Model) {
	t.Helper()
	if err := m.controller.StartWithPolicy("pending policy work", run.CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, session := range m.sessions {
			if session.State() == runtime.StateRunning {
				return true
			}
		}
		return false
	})
	if err := m.controller.SetCESPolicy(run.CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	m.refreshProjection()
}

func conversationModelFromConfig(t *testing.T, cfg config.Config) (Model, map[string]*fake.Session) {
	t.Helper()
	m := New(cfg, nil)
	_ = m.controller.Close()
	rt := fake.NewRuntime()
	m.controller = run.New(cfg, nil, run.Options{Runtime: rt})
	m.runEvents, _ = m.controller.Subscribe()
	names := make([]string, 0, len(*cfg.Agents))
	for name := range *cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	sessions := map[string]*fake.Session{}
	interfaces := map[string]runtime.Session{}
	for _, name := range names {
		session := fake.NewSession(name)
		sessions[name], interfaces[name] = session, session
	}
	m.sessions = interfaces
	if err := m.controller.SetSessions(interfaces); err != nil {
		t.Fatal(err)
	}
	m.workspaceName = "ghost"
	m.conversation = newConversationState()
	m.refreshProjection()
	controller := m.controller
	t.Cleanup(func() { _ = controller.Close() })
	return m, sessions
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func TestTaskCardLinesExactWidth(t *testing.T) {
	for _, width := range []int{8, 20, 78, 96, 140} {
		for _, title := range []string{"Task: short", "Task: " + strings.Repeat("long goal ", 30)} {
			lines := taskCardLines(title, "VEIL · completed", width)
			for i, line := range lines {
				if got := ansi.StringWidth(line); got != width {
					t.Fatalf("width %d line %d = %d cells: %q", width, i, got, line)
				}
			}
		}
	}
	if lines := taskCardLines("Task: x", "VEIL", 5); len(lines) != 2 {
		t.Fatalf("narrow fallback = %q", lines)
	}
}
