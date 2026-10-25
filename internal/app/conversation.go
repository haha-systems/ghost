package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/run"
)

type conversationFocus uint8

const (
	focusConversation conversationFocus = iota
	focusComposer
	focusEvents
	focusMemory
	focusDecisions
)

type conversationRowKind uint8

const (
	rowMessage conversationRowKind = iota
	rowTask
	rowDecision
	rowPhase
	rowFailure
	rowTools
)

type conversationRow struct {
	kind    conversationRowKind
	at      timeStamp
	message *core.Message
	task    *core.Task
	event   *core.Event
	text    string
	key     string
}

// timeStamp keeps presentation rows independent of source-specific event types.
type timeStamp = time.Time

type conversationState struct {
	tab                  int
	overflowAgent        string
	focus                conversationFocus
	composer             string
	selected             map[string]int
	scroll               map[string]int
	rows                 []conversationRow
	rowsDirty            bool
	taskGoals            map[core.TaskID]string
	taskDetail           core.TaskID
	eventsOpen           bool
	memoryOpen           bool
	decisionsOpen        bool
	decisionSelected     int
	decisionDetailID     core.EventID
	decisionDetailScroll int
	eventAgentOn         bool
	eventCategory        int
	eventSearch          string
	searchMode           bool
	eventFollow          bool
	conversationFollow   bool
	eventSelected        int
	expandedEvent        core.EventID
	filteredEvents       []core.Event
	eventIDs             map[core.EventID]struct{}
	toolCounts           map[string]int
	paletteOpen          bool
	paletteFilter        string
	paletteSelected      int
	memorySelected       int
	memoryDetailScroll   int
	memoryDetailID       string
	lastMemoryQuery      string
	lastMemoryAgent      core.AgentID
	lastMemoryTask       core.TaskID
	lastMemoryResult     *core.MemoryRecallResult
	lastMemoryWrite      string
	helpOpen             bool
	notice               string
}

func newConversationState() conversationState {
	return conversationState{
		focus: focusConversation, selected: map[string]int{}, scroll: map[string]int{},
		taskGoals: map[core.TaskID]string{}, eventAgentOn: true, eventFollow: true, conversationFollow: true,
		toolCounts: map[string]int{}, eventIDs: map[core.EventID]struct{}{}, rowsDirty: true,
	}
}

func (m *Model) refreshProjection() {
	if m.controller == nil {
		return
	}
	m.projection = m.controller.WorkspaceSnapshot()
	m.runStatus = m.controller.Status()
	m.conversation.taskGoals = map[core.TaskID]string{}
	m.conversation.eventIDs = make(map[core.EventID]struct{}, len(m.projection.Events))
	for _, item := range m.projection.Events {
		m.conversation.eventIDs[item.ID] = struct{}{}
		m.captureTaskGoal(item)
	}
	m.conversation.rowsDirty = true
	m.rebuildConversationRows()
	m.refreshFilteredEvents()
}

func (m *Model) applyCoreEvent(item core.Event) {
	if _, exists := m.conversation.eventIDs[item.ID]; exists {
		return
	}
	if item.Category == core.CategoryMemory {
		m.refreshProjection()
		return
	}
	m.conversation.eventIDs[item.ID] = struct{}{}
	m.projection.Events = append(m.projection.Events, item)
	m.captureTaskGoal(item)
	switch item.Type {
	case "conversation.human_message", "conversation.agent_message":
		var payload struct {
			Message core.Message `json:"message"`
		}
		if json.Unmarshal(item.Payload, &payload) == nil && payload.Message.ID != "" {
			m.upsertMessage(payload.Message)
			if m.conversation.rowsDirty {
				break
			}
			if agent := m.selectedAgent(); agent == nil || messageForAgent(payload.Message, agent.ID) {
				key := m.tabKey()
				selected, exists := m.conversation.selected[key]
				wasFollowing := !exists || selected >= len(m.conversation.rows)-1
				copy := payload.Message
				m.conversation.rows = append(m.conversation.rows, conversationRow{kind: rowMessage, at: copy.CreatedAt, message: &copy})
				if wasFollowing {
					m.conversation.selected[key] = len(m.conversation.rows) - 1
				}
			}
		}
	case "message.accepted":
		var payload struct {
			Message core.Message `json:"message"`
		}
		if json.Unmarshal(item.Payload, &payload) == nil && payload.Message.ID != "" {
			m.upsertMessage(payload.Message)
			m.conversation.rowsDirty = true
		}
	case "message.delivered", "message.failed":
		m.refreshProjection()
		return
	case "task.status_changed", "task.owner_changed":
		m.refreshProjection()
		return
	case "tool.call":
		key := string(item.AgentID) + ":" + string(item.TaskID)
		m.conversation.toolCounts[key]++
		if !m.conversation.rowsDirty {
			found := false
			for i := range m.conversation.rows {
				if m.conversation.rows[i].key == key {
					m.conversation.rows[i].text = fmt.Sprintf("%s ran %d tools", m.agentLabel(item.AgentID), m.conversation.toolCounts[key])
					m.conversation.rows[i].at = item.Time
					found = true
					break
				}
			}
			if !found {
				copy := item
				m.conversation.rows = append(m.conversation.rows, conversationRow{kind: rowTools, at: item.Time, event: &copy, text: fmt.Sprintf("%s ran %d tools", m.agentLabel(item.AgentID), m.conversation.toolCounts[key]), key: key})
			}
		}
	case "tool.result", "tool.file_change":
		// Details stay in the events panel. They do not add another chat row.
	case "decision.qac", "decision.qac_error", "ces.phase", "provider.error", "coordination.routing_rejected":
		m.conversation.rowsDirty = true
	}
	if m.eventMatches(item) {
		m.conversation.filteredEvents = append(m.conversation.filteredEvents, item)
		if m.conversation.eventFollow {
			m.conversation.eventSelected = len(m.conversation.filteredEvents) - 1
		}
	}
	if m.conversation.rowsDirty {
		m.rebuildConversationRows()
	}
}

func (m *Model) captureTaskGoal(item core.Event) {
	if item.Type != "task.status_changed" {
		return
	}
	var payload struct {
		TaskID core.TaskID `json:"task_id"`
		Goal   string      `json:"goal"`
	}
	if json.Unmarshal(item.Payload, &payload) == nil && payload.TaskID != "" && payload.Goal != "" {
		m.conversation.taskGoals[payload.TaskID] = payload.Goal
	}
}

func (m *Model) upsertMessage(message core.Message) {
	for i := range m.projection.Messages {
		if m.projection.Messages[i].ID == message.ID {
			m.projection.Messages[i] = message
			return
		}
	}
	m.projection.Messages = append(m.projection.Messages, message)
}

func (m *Model) rebuildConversationRows() {
	if !m.conversation.rowsDirty {
		return
	}
	rows := make([]conversationRow, 0, len(m.projection.Messages)+len(m.projection.Tasks)+16)
	agent := m.selectedAgent()
	for i := range m.projection.Messages {
		message := m.projection.Messages[i]
		if agent != nil && !messageForAgent(message, agent.ID) {
			continue
		}
		copy := message
		rows = append(rows, conversationRow{kind: rowMessage, at: message.CreatedAt, message: &copy})
	}
	for i := range m.projection.Tasks {
		task := m.projection.Tasks[i]
		if agent != nil && !taskForAgent(task, agent.ID) {
			continue
		}
		copy := task
		rows = append(rows, conversationRow{kind: rowTask, at: task.UpdatedAt, task: &copy})
	}
	toolGroups := map[string]conversationRow{}
	toolCounts := map[string]int{}
	for i := range m.projection.Events {
		item := m.projection.Events[i]
		if agent != nil && item.AgentID != agent.ID && item.Actor.ID != string(agent.ID) {
			continue
		}
		switch item.Type {
		case "tool.call":
			key := string(item.AgentID) + ":" + string(item.TaskID)
			toolCounts[key]++
			row := toolGroups[key]
			row.kind, row.at, row.event, row.key = rowTools, item.Time, &item, key
			row.text = fmt.Sprintf("%s ran %d tools", m.agentLabel(item.AgentID), toolCounts[key])
			toolGroups[key] = row
		case "decision.qac":
			rows = append(rows, conversationRow{kind: rowDecision, at: item.Time, event: &item, text: decisionSummary(item)})
		case "ces.phase":
			rows = append(rows, conversationRow{kind: rowPhase, at: item.Time, event: &item, text: phaseSummary(item)})
		case "provider.error", "decision.qac_error":
			rows = append(rows, conversationRow{kind: rowFailure, at: item.Time, event: &item, text: m.failureSummary(item)})
		case "coordination.routing_rejected":
			rows = append(rows, conversationRow{kind: rowFailure, at: item.Time, event: &item, text: m.routingRejectionSummary(item)})
		}
	}
	for _, row := range toolGroups {
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	m.conversation.rows = rows
	m.conversation.toolCounts = toolCounts
	m.conversation.rowsDirty = false
	key := m.tabKey()
	if len(rows) > 0 {
		if _, ok := m.conversation.selected[key]; !ok {
			m.conversation.selected[key] = len(rows) - 1
		}
	}
}

func messageForAgent(message core.Message, id core.AgentID) bool {
	if message.Sender == id || message.Type == core.MessageBroadcast {
		return true
	}
	for _, recipient := range message.Recipients {
		if recipient == id {
			return true
		}
	}
	return false
}

func (m Model) messageDeliveryText(message core.Message) string {
	if len(message.RecipientDelivery) == 0 {
		return string(message.Delivery)
	}
	recipients := make([]string, 0, len(message.RecipientDelivery))
	for recipient := range message.RecipientDelivery {
		recipients = append(recipients, string(recipient))
	}
	sort.Strings(recipients)
	states := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		id := core.AgentID(recipient)
		state := string(message.RecipientDelivery[id])
		if state == string(core.DeliveryFailed) && message.DeliveryReason[id] != "" {
			state += ": " + message.DeliveryReason[id]
		}
		states = append(states, strings.ToLower(m.agentLabel(id))+" "+state)
	}
	if len(states) == 1 {
		return states[0][strings.Index(states[0], " ")+1:]
	}
	return strings.Join(states, ", ")
}

func messageCardKind(message core.Message, all []core.Message) (string, core.Message) {
	switch message.Type {
	case core.MessageTaskDelegation:
		return "DELEGATION", message
	case core.MessageTaskResult:
		return "RESULT", message
	case core.MessageRequest:
		var payload struct {
			ConsultationID string `json:"consultation_id"`
		}
		if json.Unmarshal([]byte(message.Content), &payload) == nil && payload.ConsultationID != "" {
			return "CONSULTATION", message
		}
	case core.MessageReply:
		var payload struct {
			Findings    string `json:"findings"`
			Uncertainty string `json:"uncertainty"`
		}
		if message.ReplyTo != nil && json.Unmarshal([]byte(message.Content), &payload) == nil && payload.Findings != "" {
			for _, candidate := range all {
				if candidate.ID == *message.ReplyTo {
					var request struct {
						ConsultationID string `json:"consultation_id"`
					}
					if json.Unmarshal([]byte(candidate.Content), &request) == nil && request.ConsultationID != "" {
						return "CONSULT RESULT", candidate
					}
				}
			}
		}
	}
	return "", core.Message{}
}

func (m Model) messageParent(message, related core.Message) string {
	taskID := related.TaskID
	if taskID == nil {
		taskID = message.TaskID
	}
	if taskID == nil {
		return ""
	}
	for _, task := range m.projection.Tasks {
		if task.ID != *taskID {
			continue
		}
		if task.Parent != nil {
			return string(*task.Parent)
		}
		if related.Type == core.MessageRequest {
			return string(task.ID)
		}
	}
	return ""
}

func shortTaskID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func taskForAgent(task core.Task, id core.AgentID) bool {
	if task.Owner == id {
		return true
	}
	for _, participant := range task.Participants {
		if participant == id {
			return true
		}
	}
	return false
}

func decisionSummary(item core.Event) string {
	var payload map[string]any
	_ = json.Unmarshal(item.Payload, &payload)
	from, to, action := value(payload, "from"), value(payload, "to"), value(payload, "action")
	parts := []string{"QAC decision"}
	if from != "" || to != "" {
		parts = append(parts, strings.TrimSpace(from+" → "+to))
	}
	if action != "" {
		parts = append(parts, action)
	}
	return strings.Join(parts, " · ")
}

func phaseSummary(item core.Event) string {
	var payload map[string]any
	_ = json.Unmarshal(item.Payload, &payload)
	phase := firstValue(payload, "phase", "name", "to", "status")
	if phase == "" {
		return "CES phase changed"
	}
	return "CES phase · " + strings.ToUpper(phase)
}

func (m Model) failureSummary(item core.Event) string {
	var payload map[string]any
	_ = json.Unmarshal(item.Payload, &payload)
	message := firstValue(payload, "error", "message", "summary")
	for _, nested := range []string{"runtime", "event"} {
		if child, ok := payload[nested].(map[string]any); ok && message == "" {
			message = firstValue(child, "error", "message", "summary")
		}
	}
	if message == "" {
		message = item.Type
	}
	actor := m.agentLabel(item.AgentID)
	if actor == "" || actor == strings.ToUpper(string(item.AgentID)) {
		if item.Source != "" {
			actor = strings.ToUpper(item.Source)
		} else if item.Actor.ID != "" {
			actor = strings.ToUpper(item.Actor.ID)
		} else {
			actor = "GHOST"
		}
	}
	return "! " + actor + " · " + message
}

func (m Model) routingRejectionSummary(item core.Event) string {
	var payload struct {
		Sender    core.AgentID `json:"sender"`
		Action    string       `json:"action"`
		Recipient string       `json:"recipient"`
		Reason    string       `json:"reason"`
		TaskID    core.TaskID  `json:"task_id"`
	}
	_ = json.Unmarshal(item.Payload, &payload)
	if payload.Sender == "" {
		payload.Sender = item.AgentID
	}
	if payload.Sender == "" && item.Actor.Kind == core.ActorAgent {
		payload.Sender = core.AgentID(item.Actor.ID)
	}
	if payload.Action == "" {
		payload.Action = "request"
	}
	if payload.Recipient == "" {
		payload.Recipient = "unknown recipient"
	}
	if payload.TaskID == "" {
		payload.TaskID = item.TaskID
	}
	actor := m.agentLabel(payload.Sender)
	return fmt.Sprintf("routing rejected: %s %s -> %s (task %s): %s", actor, payload.Action, payload.Recipient, payload.TaskID, payload.Reason)
}

func value(payload map[string]any, key string) string {
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}

func firstValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := value(payload, key); text != "" {
			return text
		}
	}
	return ""
}

func (m Model) selectedAgent() *core.Agent {
	name := m.selectedAgentName()
	if name == "" {
		return nil
	}
	for i := range m.projection.Agents {
		if strings.EqualFold(m.projection.Agents[i].DisplayName, name) {
			return &m.projection.Agents[i]
		}
	}
	return nil
}

func (m Model) selectedMemoryTask() *core.Task {
	taskID := m.conversation.taskDetail
	if taskID == "" {
		rows := m.conversation.rows
		index, selected := m.conversation.selected[m.tabKey()]
		if selected && index >= 0 && index < len(rows) && rows[index].task != nil {
			taskID = rows[index].task.ID
		}
	}
	if taskID == "" {
		return nil
	}
	for i := range m.projection.Tasks {
		if m.projection.Tasks[i].ID == taskID {
			return &m.projection.Tasks[i]
		}
	}
	return nil
}

func (m Model) selectedAgentName() string {
	if m.conversation.overflowAgent != "" {
		return m.conversation.overflowAgent
	}
	if m.conversation.tab <= 0 || m.conversation.tab > 9 || m.conversation.tab > len(m.projection.Agents) {
		return ""
	}
	return m.projection.Agents[m.conversation.tab-1].DisplayName
}

func (m Model) tabKey() string {
	if name := m.selectedAgentName(); name != "" {
		return "agent:" + strings.ToLower(name)
	}
	return "all"
}

func (m Model) defaultRecipient() string {
	if name := m.selectedAgentName(); name != "" {
		return "@" + name
	}
	return "@all"
}

func (m Model) agentLabel(id core.AgentID) string {
	for _, agent := range m.projection.Agents {
		if agent.ID == id {
			return strings.ToUpper(agent.DisplayName)
		}
	}
	return strings.ToUpper(string(id))
}

func (m Model) agentByName(name string) (core.Agent, bool) {
	for _, agent := range m.projection.Agents {
		if strings.EqualFold(agent.DisplayName, name) {
			return agent, true
		}
	}
	return core.Agent{}, false
}

func (m *Model) refreshFilteredEvents() {
	filtered := make([]core.Event, 0, len(m.projection.Events))
	for _, item := range m.projection.Events {
		if m.eventMatches(item) {
			filtered = append(filtered, item)
		}
	}
	m.conversation.filteredEvents = filtered
	if m.conversation.eventFollow {
		m.conversation.eventSelected = len(filtered) - 1
	} else if m.conversation.eventSelected >= len(filtered) {
		m.conversation.eventSelected = len(filtered) - 1
	}
}

func (m Model) eventMatches(item core.Event) bool {
	if m.conversation.eventCategory > 0 && item.Category != eventCategories[m.conversation.eventCategory-1] {
		return false
	}
	if name := m.selectedAgentName(); name != "" && m.conversation.eventAgentOn {
		if agent, ok := m.agentByName(name); ok && item.AgentID != agent.ID && item.Actor.ID != string(agent.ID) {
			return false
		}
	}
	search := strings.ToLower(strings.TrimSpace(m.conversation.eventSearch))
	searchable := strings.Join([]string{
		string(item.Payload), item.Type, item.Source, item.Actor.ID,
		m.agentLabel(item.AgentID), string(item.Category),
	}, " ")
	if search != "" && !strings.Contains(strings.ToLower(searchable), search) {
		return false
	}
	return true
}

var eventCategories = []core.Category{
	core.CategoryConversation, core.CategoryTask, core.CategoryTool,
	core.CategoryCoordination, core.CategoryMemory, core.CategoryDecision,
}

func (m *Model) toggleEvents() {
	m.conversation.eventsOpen = !m.conversation.eventsOpen
	if m.conversation.eventsOpen {
		m.conversation.memoryOpen = false
		m.conversation.decisionsOpen = false
		m.conversation.focus = focusEvents
		m.refreshFilteredEvents()
	} else {
		m.conversation.focus = focusConversation
		m.conversation.expandedEvent = ""
	}
}

func (m *Model) toggleMemory() {
	if m.conversation.memoryOpen {
		m.conversation.memoryOpen = false
		m.conversation.memoryDetailID = ""
		m.conversation.memoryDetailScroll = 0
		m.conversation.focus = focusConversation
		return
	}
	m.openMemory()
}

func (m *Model) openMemory() {
	m.conversation.memoryOpen = true
	m.conversation.eventsOpen = false
	m.conversation.decisionsOpen = false
	m.conversation.focus = focusMemory
}

func (m *Model) toggleDecisions() {
	if m.conversation.decisionsOpen {
		m.conversation.decisionsOpen = false
		m.conversation.decisionDetailID = ""
		m.conversation.decisionDetailScroll = 0
		m.conversation.focus = focusConversation
		return
	}
	m.conversation.decisionsOpen = true
	m.conversation.memoryOpen = false
	m.conversation.eventsOpen = false
	m.conversation.decisionSelected = 0
	m.conversation.decisionDetailID = ""
	m.conversation.decisionDetailScroll = 0
	m.conversation.focus = focusDecisions
}

func (m *Model) chooseTab(index int) {
	m.conversation.tab = index
	m.conversation.overflowAgent = ""
	if index > len(m.projection.Agents) {
		m.conversation.tab = 0
	}
	m.conversation.rowsDirty = true
	m.rebuildConversationRows()
	m.refreshFilteredEvents()
}

func (m *Model) chooseAgent(name string) error {
	for index, agent := range m.projection.Agents {
		if strings.EqualFold(agent.DisplayName, name) {
			if index < 9 {
				m.chooseTab(index + 1)
			} else {
				m.conversation.tab = 0
				m.conversation.overflowAgent = agent.DisplayName
				m.conversation.rowsDirty = true
				m.rebuildConversationRows()
				m.refreshFilteredEvents()
			}
			return nil
		}
	}
	return fmt.Errorf("unknown agent %q", name)
}

func (m Model) renderConversationView() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	c := m.conversation
	top := m.styled("GHOST / BLOODWIRE", m.theme.Colors.Text, true)
	status := m.cesStatusLabel()
	// Decision availability only matters when CES auto is requested; showing
	// it otherwise would flag the default (decisions disabled) as a fault.
	if m.runStatus.Requested == run.CESPolicyAuto || m.runStatus.Effective == run.CESPolicyAuto {
		status += " · decision service " + availabilityLabel(m.runStatus.DecisionAvailable)
	}
	head := " " + top
	statusText := m.styled(status, m.theme.Colors.Accent, true)
	statusOnOwnLine := ansi.StringWidth(head)+ansi.StringWidth(statusText)+1 >= m.width
	if !statusOnOwnLine {
		head = m.putRight(head, statusText)
	}
	head = m.pad(head, m.width)
	workspace := fmt.Sprintf("workspace: %s · %d AGENTS", m.workspaceName, len(m.projection.Agents))
	if recovery := m.runStatus.Recovery; recovery.FilesRead > 0 || recovery.FilesSkipped > 0 || recovery.FilesOmitted > 0 || recovery.EventsOmitted > 0 {
		workspace += fmt.Sprintf(" · recovered %s, %s", recoveryCount(recovery.MessagesRestored, "message"), recoveryCount(recovery.TasksRestored, "task"))
	}
	tabs := m.tabsLines()
	title := "SHARED CONVERSATION"
	if c.memoryOpen && m.width < 120 {
		title = "MEMORY"
	} else if c.decisionsOpen && m.width < 120 {
		title = "DECISIONS"
	} else if c.taskDetail != "" {
		title = "TASK DETAIL"
	} else if c.eventsOpen && m.width < 120 {
		title = "EVENTS"
	}
	titleLine := m.styled(title, m.theme.Colors.TextMuted, true)
	divider := m.styled(strings.Repeat("─", max(1, m.width)), m.theme.Colors.Border, false)
	header := []string{head}
	if statusOnOwnLine {
		header = append(header, m.pad(statusText, m.width))
	}
	header = append(header, m.pad(workspace, m.width))
	for _, line := range tabs {
		header = append(header, m.pad(line, m.width))
	}
	if agent := m.selectedAgent(); agent != nil && agent.Activity != "" {
		header = append(header, m.pad("ACTIVITY · "+agent.Activity, m.width))
	}
	bodyHeight := max(0, m.height-len(header)-5)
	var body []string
	switch {
	case c.memoryOpen && m.width < 120:
		body = m.memoryLines(bodyHeight, m.width)
	case c.decisionsOpen && m.width < 120:
		body = m.decisionLines(bodyHeight, m.width)
	case c.taskDetail != "" && !c.memoryOpen:
		body = m.taskDetailLines(bodyHeight, m.width)
	case c.eventsOpen && m.width < 120:
		body = m.eventLines(bodyHeight, m.width, true)
	default:
		mainWidth := m.width
		var side []string
		if (c.eventsOpen || c.memoryOpen || c.decisionsOpen) && m.width >= 120 {
			sideWidth := min(38, m.width/3)
			mainWidth = m.width - sideWidth - 3
			if c.memoryOpen {
				side = m.memoryLines(bodyHeight, sideWidth)
			} else if c.decisionsOpen {
				side = m.decisionLines(bodyHeight, sideWidth)
			} else {
				side = m.eventLines(bodyHeight, sideWidth, false)
			}
		}
		main := m.conversationLines(bodyHeight, mainWidth)
		if c.taskDetail != "" {
			main = m.taskDetailLines(bodyHeight, mainWidth)
		}
		if len(side) > 0 {
			body = make([]string, bodyHeight)
			for i := range body {
				var right string
				if i < len(side) {
					right = side[i]
				}
				var left string
				if i < len(main) {
					left = main[i]
				}
				body[i] = m.pad(left, mainWidth) + " │ " + m.pad(right, m.width-mainWidth-3)
			}
		} else {
			body = main
		}
	}
	lines := append(header, divider, m.pad(titleLine, m.width))
	for i := 0; i < bodyHeight; i++ {
		if i < len(body) {
			lines = append(lines, m.pad(body[i], m.width))
		} else {
			lines = append(lines, "")
		}
	}
	lines = append(lines, divider, m.composerLine(), m.footerLine())
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	content := strings.Join(lines, "\n")
	if c.paletteOpen {
		content = m.paletteView(content)
	} else if c.helpOpen {
		content = m.helpView(content)
	}
	return content
}

func (m Model) cesStatusLabel() string {
	status := m.runStatus
	if status.Requested == run.CESPolicyAuto {
		label := "CES AUTO"
		switch {
		case status.CESAuto.Pending:
			label += " · decision pending"
			if status.CESAuto.Source != "" {
				label += " · source " + status.CESAuto.Source
			}
		case status.Pending:
			label += " · policy pending until next Start"
		case status.CESAuto.Executed != "":
			if status.CESAuto.Recommended != "" {
				label += " · recommended " + status.CESAuto.Recommended
			}
			label += " · executed " + status.CESAuto.Executed
			if status.CESAuto.Source != "" {
				label += " · " + status.CESAuto.Source
			}
			if status.CESAuto.Reason != "" {
				label += " · " + status.CESAuto.Reason
			}
		case status.CESAuto.Reason != "":
			label += " · " + status.CESAuto.Reason
		default:
			label += " · next task"
		}
		return label
	}
	if status.Pending {
		if status.Requested == run.CESPolicyOn {
			return "CES ON (pending)"
		}
		return "CES OFF (pending)"
	}
	if status.Requested == run.CESPolicyOn && status.Effective == run.CESPolicyOff && status.PolicyReason != "" {
		return "CES OFF — " + status.PolicyReason
	}
	if status.Effective == run.CESPolicyOn {
		return "CES ON"
	}
	return "CES OFF"
}

func availabilityLabel(available bool) string {
	if available {
		return "available"
	}
	return "unavailable"
}

func (m Model) tabsLines() []string {
	all := "[0 ALL]"
	if m.conversation.tab == 0 && m.conversation.overflowAgent == "" {
		all = "› " + all
	}
	parts := []string{all}
	for i := 0; i < min(9, len(m.projection.Agents)); i++ {
		a := m.projection.Agents[i]
		marker := "○ idle"
		switch a.Availability {
		case core.AvailabilityBusy:
			marker = "● busy"
		case core.AvailabilityAvailable:
			marker = "● idle"
		case core.AvailabilityError:
			marker = "! error"
		case core.AvailabilityOffline:
			marker = "○ offline"
		}
		tab := fmt.Sprintf("[%d %s %s]", i+1, strings.ToUpper(a.DisplayName), marker)
		if m.conversation.tab == i+1 && m.conversation.overflowAgent == "" {
			tab = "› " + tab
		}
		parts = append(parts, tab)
	}
	if overflow := len(m.projection.Agents) - 9; overflow > 0 {
		label := fmt.Sprintf("+%d", overflow)
		if m.conversation.overflowAgent != "" {
			label += " " + strings.ToUpper(m.conversation.overflowAgent)
		}
		if m.conversation.overflowAgent != "" {
			label = "› " + label
		}
		parts = append(parts, "["+label+"]")
	}
	lines := make([]string, 0, 2)
	line := ""
	for _, part := range parts {
		part = ansi.Truncate(part, m.width, "…")
		if line != "" && ansi.StringWidth(line)+1+ansi.StringWidth(part) > m.width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += part
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (m Model) conversationLines(height, width int) []string {
	rows := m.conversation.rows
	key := m.tabKey()
	start := m.conversation.scroll[key]
	if start < 0 {
		start = max(0, len(rows)-height)
	}
	selected, hasSelected := m.conversation.selected[key]
	if hasSelected && selected < start {
		start = selected
	}
	if hasSelected && selected >= start+height {
		start = selected - height + 1
	}
	if height <= 0 || start >= len(rows) {
		if m.conversation.notice != "" && height > 0 {
			return []string{ansi.Truncate(m.conversation.notice, width, "…")}
		}
		return nil
	}
	var lines []string
	for i := start; i < len(rows) && len(lines) < height; i++ {
		prefix := "  "
		if hasSelected && i == selected && m.conversation.focus == focusConversation {
			prefix = "› "
		}
		for j, line := range m.rowLines(rows[i], width-2) {
			if j == 0 {
				lines = append(lines, prefix+line)
			} else {
				lines = append(lines, "  "+line)
			}
			if len(lines) >= height {
				break
			}
		}
	}
	if m.conversation.notice != "" && len(lines) > 0 {
		lines[0] = ansi.Truncate("! "+m.conversation.notice, width, "…")
	}
	return lines
}

func (m Model) rowLines(row conversationRow, width int) []string {
	if width < 1 {
		return []string{""}
	}
	switch row.kind {
	case rowMessage:
		message := row.message
		if message == nil {
			return nil
		}
		var heading string
		if message.Type == core.MessageSystem {
			targets := make([]string, 0, len(message.Recipients))
			for _, id := range message.Recipients {
				targets = append(targets, "@"+strings.ToLower(m.agentLabel(id)))
			}
			heading = "GHOST → " + strings.Join(targets, " ")
		} else if message.Sender == core.HumanAgentID {
			target := "@all"
			if message.Type != core.MessageBroadcast && len(message.Recipients) > 0 {
				names := make([]string, 0, len(message.Recipients))
				for _, id := range message.Recipients {
					names = append(names, "@"+strings.ToLower(m.agentLabel(id)))
				}
				target = strings.Join(names, " ")
			}
			heading = "YOU → " + target
		} else {
			targets := make([]string, 0, len(message.Recipients))
			for _, id := range message.Recipients {
				targets = append(targets, "@"+strings.ToLower(m.agentLabel(id)))
			}
			heading = m.agentLabel(message.Sender) + " → " + strings.Join(targets, " ")
		}
		heading += " · " + m.messageDeliveryText(*message)
		if kind, related := messageCardKind(*message, m.projection.Messages); kind != "" {
			title := kind + " · " + m.messageDeliveryText(*message)
			if parent := m.messageParent(*message, related); parent != "" {
				title += " · ↳ parent " + shortTaskID(parent)
			}
			return taskCardLines(title, heading+": "+message.Content, width)
		}
		wrapped := strings.Split(ansi.Hardwrap(message.Content, max(1, width-2), false), "\n")
		return append([]string{m.styled(heading, m.theme.Colors.Accent, true)}, wrapped...)
	case rowTask:
		if row.task == nil {
			return nil
		}
		task := row.task
		goal := m.conversation.taskGoals[task.ID]
		if goal == "" {
			goal = task.Objective
		}
		if goal == "" {
			goal = "Task " + string(task.ID)
		}
		owner := m.agentLabel(task.Owner)
		state := owner + " · " + string(task.Status)
		if task.FailureReason != "" {
			state += " · " + task.FailureReason
		}
		return taskCardLines("Task: "+goal, state, width)
	case rowDecision, rowPhase, rowFailure, rowTools:
		return []string{ansi.Truncate(m.styled(row.text, m.rowColor(row.kind), row.kind != rowTools), width, "…")}
	default:
		return nil
	}
}

func (m Model) rowColor(kind conversationRowKind) string {
	if kind == rowFailure {
		return m.theme.Colors.Error
	}
	if kind == rowDecision || kind == rowPhase {
		return m.theme.Colors.Accent
	}
	return m.theme.Colors.TextMuted
}

func (m Model) taskDetailLines(height, width int) []string {
	var task *core.Task
	for i := range m.projection.Tasks {
		if m.projection.Tasks[i].ID == m.conversation.taskDetail {
			task = &m.projection.Tasks[i]
			break
		}
	}
	if task == nil {
		return []string{"Task is no longer in the workspace."}
	}
	owner := m.agentLabel(task.Owner)
	participants := make([]string, 0, len(task.Participants))
	for _, id := range task.Participants {
		participants = append(participants, m.agentLabel(id))
	}
	parent := "—"
	if task.Parent != nil {
		parent = string(*task.Parent)
	}
	children := make([]string, 0)
	for _, candidate := range m.projection.Tasks {
		if candidate.Parent != nil && *candidate.Parent == task.ID {
			children = append(children, string(candidate.ID))
		}
	}
	goal := m.conversation.taskGoals[task.ID]
	if goal == "" {
		goal = "Task " + string(task.ID)
	}
	deps := make([]string, len(task.DependsOn))
	for i, id := range task.DependsOn {
		deps[i] = string(id)
	}
	lines := []string{
		"Task: " + goal,
		"Owner: " + owner,
		"Participants: " + strings.Join(participants, ", "),
		"Status: " + string(task.Status),
		"Parent: " + parent,
		"Children: " + valueOr(strings.Join(children, ", "), "—"),
		"Dependencies: " + valueOr(strings.Join(deps, ", "), "—"),
		"CES policy: " + string(task.CESPolicy),
		"Related messages:",
	}
	if !task.Status.IsTerminal() {
		lines = append(lines, "Cancel: press x")
	}
	relatedTasks := map[core.TaskID]bool{task.ID: true}
	for changed := true; changed; {
		changed = false
		for _, candidate := range m.projection.Tasks {
			if candidate.Parent != nil && relatedTasks[*candidate.Parent] && !relatedTasks[candidate.ID] {
				relatedTasks[candidate.ID] = true
				changed = true
			}
		}
	}
	for _, message := range m.projection.Messages {
		if message.TaskID != nil && relatedTasks[*message.TaskID] {
			prefix := "  "
			if *message.TaskID != task.ID {
				prefix = "  ↳ " + shortTaskID(string(*message.TaskID)) + " "
			}
			lines = append(lines, prefix+m.agentLabel(message.Sender)+": "+message.Content+" · "+m.messageDeliveryText(message))
		}
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return lines[:min(height, len(lines))]
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (m Model) eventLines(height, width int, overlay bool) []string {
	events := m.conversation.filteredEvents
	if height <= 0 || width <= 0 {
		return nil
	}
	selected := m.conversation.eventSelected
	start := max(0, len(events)-height)
	if !m.conversation.eventFollow && selected >= 0 && selected < len(events) && selected < start {
		start = selected
	}
	lines := make([]string, 0, height)
	filter := "agent=all"
	if name := m.selectedAgentName(); name != "" && m.conversation.eventAgentOn {
		filter = "agent=" + name
	} else if m.selectedAgentName() != "" {
		filter = "agent=all"
	}
	category := "all"
	if m.conversation.eventCategory > 0 {
		category = string(eventCategories[m.conversation.eventCategory-1])
	}
	follow := "following"
	if !m.conversation.eventFollow {
		follow = "paused"
	}
	lines = append(lines, ansi.Truncate(fmt.Sprintf("%s · %s · %s", filter, category, follow), width, "…"))
	if m.conversation.searchMode || m.conversation.eventSearch != "" {
		lines = append(lines, ansi.Truncate("/ "+m.conversation.eventSearch, width, "…"))
	}
	for i := start; i < len(events) && len(lines) < height; i++ {
		item := events[i]
		marker := "  "
		if i == selected && m.conversation.focus == focusEvents {
			marker = "› "
		}
		actor := item.Source
		if item.Actor.Kind == core.ActorAgent {
			actor = m.agentLabel(core.AgentID(item.Actor.ID))
		}
		line := fmt.Sprintf("%s%05d %s %s", marker, item.Seq, actor, item.Type)
		switch item.Type {
		case "coordination.routing_rejected":
			line = fmt.Sprintf("%s%05d %s", marker, item.Seq, m.routingRejectionSummary(item))
		case "workspace.recovered":
			var summary run.RecoverySummary
			if json.Unmarshal(item.Payload, &summary) == nil {
				line += fmt.Sprintf(" · %s · %s · %s · %s · %s · %s · %s · %s · %s · %s · %s",
					recoveryCount(summary.FilesRead, "file read"), recoveryCount(summary.FilesSkipped, "file skipped"),
					recoveryCount(summary.FilesOmitted, "file omitted"), recoveryCount(summary.EventsRead, "event restored"),
					recoveryCount(summary.EventsOmitted, "event omitted"), recoveryCount(summary.MessagesRestored, "message restored"),
					recoveryCount(summary.TasksRestored, "task restored"), recoveryCount(summary.ReconciledTasks, "interrupted task"),
					recoveryCount(summary.UncertainDeliveries, "uncertain delivery"), recoveryCount(summary.PendingMessages, "pending message"),
					recoveryCount(summary.SkippedTailLines, "truncated line"))
			}
		case "workspace.recovery_warning":
			var warning struct {
				Warning string `json:"warning"`
			}
			if json.Unmarshal(item.Payload, &warning) == nil && warning.Warning != "" {
				line += " · " + warning.Warning
			}
		}
		lines = append(lines, ansi.Truncate(line, width, "…"))
		if item.ID == m.conversation.expandedEvent {
			var pretty bytes.Buffer
			if json.Indent(&pretty, item.Payload, "", "  ") == nil {
				for _, raw := range strings.Split(pretty.String(), "\n") {
					if len(lines) >= height {
						break
					}
					lines = append(lines, ansi.Truncate("  "+raw, width, "…"))
				}
			}
		}
	}
	if len(events) == 0 {
		lines = append(lines, "No events")
	}
	if overlay && m.conversation.notice != "" && len(lines) > 1 {
		lines[1] = ansi.Truncate("! "+m.conversation.notice, width, "…")
	}
	return lines[:min(height, len(lines))]
}

func recoveryCount(count int, label string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", label)
	}
	plural := label + "s"
	if strings.HasSuffix(label, "y") {
		plural = strings.TrimSuffix(label, "y") + "ies"
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func (m Model) composerLine() string {
	return ansi.Truncate(m.styled("> "+m.defaultRecipient()+" "+m.conversation.composer, m.theme.Colors.Text, m.conversation.focus == focusComposer), m.width, "…")
}

func (m Model) footerLine() string {
	if m.conversation.taskDetail != "" {
		return ansi.Truncate("x cancel task  Esc back  ^E events  ^C quit", m.width, "…")
	}
	return ansi.Truncate("0-9 agents  ^E events  ^P commands  :cancel [TASK-ID]  c CES  Tab focus  ^C quit", m.width, "…")
}

func (m Model) styled(text, color string, bold bool) string {
	if m.colorProfile <= colorprofile.ASCII {
		return text
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(bold)
	return style.Render(text)
}

func (m Model) pad(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = ansi.Truncate(text, width, "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func (m Model) putRight(line, value string) string {
	width := ansi.StringWidth(line)
	valueWidth := ansi.StringWidth(value)
	if width+valueWidth >= m.width {
		return line
	}
	return line + strings.Repeat(" ", m.width-width-valueWidth) + value
}

func (m Model) paletteView(base string) string {
	lines := strings.Split(base, "\n")
	items := m.paletteItems()
	filtered := filterPalette(items, m.conversation.paletteFilter)
	var overlay []string
	overlay = append(overlay, "COMMANDS  "+m.conversation.paletteFilter)
	for i := range filtered {
		prefix := "  "
		if i == m.conversation.paletteSelected {
			prefix = "› "
		}
		overlay = append(overlay, prefix+filtered[i])
		if len(overlay) >= min(8, m.height-2) {
			break
		}
	}
	start := max(0, (len(lines)-len(overlay))/2)
	for i := range overlay {
		if start+i < len(lines) {
			lines[start+i] = m.pad(ansi.Truncate(overlay[i], m.width, "…"), m.width)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) helpView(base string) string {
	lines := strings.Split(base, "\n")
	help := []string{"Keys", "0-9 select agent; 0 shows all", "Ctrl+E events; / search; a agent filter; k category", "Tab focus; arrows scroll; Enter opens tasks, event data, or inspector details", "Memory and decisions inspectors: arrows select; Enter opens full details", "Task detail: x cancels that task and its children", "c cycles CES off, on, and auto; Ctrl+X cancel; Ctrl+C quit", ":memory :recall QUERY :remember TEXT :decisions", ":ces on :ces off :ces auto :ces view", ":decision override ID ACTION :auto-consult on|off", ":delegation on|off :events :agent NAME", ":cancel [TASK-ID] :legacy :quit :help", "Esc closes this help"}
	start := max(0, len(lines)-len(help)-3)
	for i, line := range help {
		if start+i < len(lines) {
			lines[start+i] = m.pad(ansi.Truncate(line, m.width, "…"), m.width)
		}
	}
	return strings.Join(lines, "\n")
}

func filterPalette(items []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), query) {
			out = append(out, item)
		}
	}
	return out
}

func workspaceLabel(path string) string {
	if path == "" || path == "." {
		return "ghost"
	}
	return filepath.Base(filepath.Clean(path))
}

// taskCardLines draws a bordered card exactly width cells wide. Narrow widths
// fall back to unbordered lines so small terminals stay legible.
func taskCardLines(title, body string, width int) []string {
	if width < 8 {
		return []string{ansi.Truncate(title, max(1, width), "…"), ansi.Truncate(body, max(1, width), "…")}
	}
	inner := width - 4 // "┌ " + " ┐" and "│ " + " │"
	title = ansi.Truncate(title, inner, "…")
	body = ansi.Truncate(body, inner, "…")
	top := "┌ " + title + " " + strings.Repeat("─", max(0, width-ansi.StringWidth(title)-4)) + "┐"
	mid := "│ " + body + strings.Repeat(" ", max(0, inner-ansi.StringWidth(body))) + " │"
	bottom := "└" + strings.Repeat("─", width-2) + "┘"
	return []string{top, mid, bottom}
}
