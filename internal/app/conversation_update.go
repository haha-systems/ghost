package app

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/run"
)

func (m Model) updateConversation(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if key.Matches(keyMsg, m.keys.OpenPalette) {
		m.conversation.paletteOpen = true
		m.conversation.paletteFilter = ""
		m.conversation.paletteSelected = 0
		return m, nil
	}
	if key.Matches(keyMsg, m.keys.ForceQuit) {
		return m, m.quitCmd()
	}
	if key.Matches(keyMsg, m.keys.Cancel) {
		if err := m.controller.Cancel(); err != nil {
			m.conversation.notice = err.Error()
		} else {
			m.refreshProjection()
			m.conversation.notice = "run cancelled"
		}
		return m, nil
	}
	if key.Matches(keyMsg, m.keys.ToggleEvents) {
		m.toggleEvents()
		return m, nil
	}
	if m.conversation.paletteOpen {
		return m.updatePalette(keyMsg)
	}
	if m.conversation.searchMode {
		return m.updateEventSearch(keyMsg)
	}
	if key.Matches(keyMsg, m.keys.Escape) {
		if m.conversation.memoryOpen {
			m.conversation.memoryOpen = false
			m.conversation.memoryDetailID = ""
			m.conversation.memoryDetailScroll = 0
			m.conversation.focus = focusConversation
		} else if m.conversation.expandedEvent != "" {
			m.conversation.expandedEvent = ""
		} else if m.conversation.taskDetail != "" {
			m.conversation.taskDetail = ""
		} else if m.conversation.eventsOpen {
			m.toggleEvents()
		} else if m.conversation.helpOpen {
			m.conversation.helpOpen = false
		} else {
			m.conversation.focus = focusConversation
		}
		return m, nil
	}
	if key.Matches(keyMsg, m.keys.Tab) {
		m.moveFocus(1)
		return m, nil
	}
	if key.Matches(keyMsg, m.keys.ShiftTab) {
		m.moveFocus(-1)
		return m, nil
	}
	if m.conversation.focus == focusComposer {
		return m.updateComposer(keyMsg)
	}
	if m.conversation.focus == focusEvents {
		return m.updateEvents(keyMsg)
	}
	if m.conversation.focus == focusMemory {
		return m.updateMemory(keyMsg)
	}
	return m.updateConversationNavigation(keyMsg)
}

func (m *Model) moveFocus(direction int) {
	order := []conversationFocus{focusComposer, focusConversation}
	if m.conversation.eventsOpen {
		order = append(order, focusEvents)
	}
	if m.conversation.memoryOpen {
		order = append(order, focusMemory)
	}
	current := 0
	for i, focus := range order {
		if focus == m.conversation.focus {
			current = i
			break
		}
	}
	current = (current + direction + len(order)) % len(order)
	m.conversation.focus = order[current]
}

func (m Model) updateConversationNavigation(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.conversation.taskDetail != "" && msg.Text == "x":
		if err := m.controller.CancelTask(m.conversation.taskDetail); err != nil {
			m.conversation.notice = err.Error()
		} else {
			m.refreshProjection()
			m.conversation.notice = "task cancelled"
		}
	case key.Matches(msg, m.keys.Up):
		m.moveConversationSelection(-1)
	case key.Matches(msg, m.keys.Down):
		m.moveConversationSelection(1)
	case key.Matches(msg, m.keys.PageUp):
		for i := 0; i < max(1, m.height/2); i++ {
			m.moveConversationSelection(-1)
		}
	case key.Matches(msg, m.keys.PageDown):
		for i := 0; i < max(1, m.height/2); i++ {
			m.moveConversationSelection(1)
		}
	case key.Matches(msg, m.keys.Home):
		m.conversation.scroll[m.tabKey()] = 0
		m.conversation.selected[m.tabKey()] = 0
	case key.Matches(msg, m.keys.End):
		m.conversation.scroll[m.tabKey()] = -1
		if len(m.conversation.rows) > 0 {
			m.conversation.selected[m.tabKey()] = len(m.conversation.rows) - 1
		}
	case key.Matches(msg, m.keys.Enter):
		key := m.tabKey()
		index, ok := m.conversation.selected[key]
		if ok && index >= 0 && index < len(m.conversation.rows) && m.conversation.rows[index].task != nil {
			m.conversation.taskDetail = m.conversation.rows[index].task.ID
		}
	case key.Matches(msg, m.keys.ToggleCES):
		m.toggleCES()
	case key.Matches(msg, m.keys.ToggleFollow):
		m.conversation.eventFollow = !m.conversation.eventFollow
		if m.conversation.eventFollow {
			m.conversation.eventSelected = len(m.conversation.filteredEvents) - 1
		}
	case key.Matches(msg, m.keys.ToggleAgent):
		if m.conversation.eventsOpen {
			m.conversation.eventAgentOn = !m.conversation.eventAgentOn
			m.refreshFilteredEvents()
		}
	case key.Matches(msg, m.keys.CycleCategory):
		if m.conversation.eventsOpen {
			m.conversation.eventCategory = (m.conversation.eventCategory + 1) % (len(eventCategories) + 1)
			m.refreshFilteredEvents()
		}
	case key.Matches(msg, m.keys.OpenEpistemic):
		m.returnScreen = ConversationScreen
		m.screen = EpistemicScreen
	case msg.Text == "/":
		m.conversation.eventsOpen = true
		m.conversation.focus = focusEvents
		m.conversation.searchMode = true
		m.conversation.eventSearch = ""
		m.refreshFilteredEvents()
	case msg.Text == "f":
		m.conversation.eventFollow = !m.conversation.eventFollow
		if m.conversation.eventFollow {
			m.conversation.eventSelected = len(m.conversation.filteredEvents) - 1
		}
	case msg.Text == "c":
		m.toggleCES()
	case len(msg.Text) == 1 && msg.Text[0] >= '0' && msg.Text[0] <= '9':
		m.chooseTab(int(msg.Text[0] - '0'))
	case key.Matches(msg, m.keys.Help):
		m.conversation.helpOpen = !m.conversation.helpOpen
	}
	return m, nil
}

func (m *Model) moveConversationSelection(delta int) {
	rows := m.conversation.rows
	if len(rows) == 0 {
		return
	}
	key := m.tabKey()
	index, exists := m.conversation.selected[key]
	if !exists {
		index = len(rows) - 1
	} else {
		index = max(0, min(len(rows)-1, index+delta))
	}
	m.conversation.selected[key] = index
	visible := max(1, m.height-8)
	start := m.conversation.scroll[key]
	if start < 0 {
		start = max(0, len(rows)-visible)
	}
	if index < start {
		start = index
	}
	if index >= start+visible {
		start = index - visible + 1
	}
	m.conversation.scroll[key] = start
}

func (m Model) updateComposer(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Enter) || (msg.Code == tea.KeyEnter && msg.Mod&tea.ModCtrl != 0) {
		return m.submitComposer()
	}
	if msg.Code == tea.KeyBackspace || msg.Code == tea.KeyDelete {
		if len(m.conversation.composer) > 0 {
			m.conversation.composer = m.conversation.composer[:len(m.conversation.composer)-1]
		}
		return m, nil
	}
	if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta) == 0 {
		m.conversation.composer += msg.Text
		m.conversation.notice = ""
	}
	return m, nil
}

func (m Model) submitComposer() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.conversation.composer)
	if text == "" {
		return m, nil
	}
	if strings.HasPrefix(text, ":") {
		m.conversation.composer = ""
		return m.executeCommand(strings.TrimSpace(strings.TrimPrefix(text, ":")))
	}
	recipients := []string{"all"}
	if name := m.selectedAgentName(); name != "" {
		recipients = []string{name}
	}
	if hasMention(text) {
		recipients = nil
	}
	if err := m.controller.Send(recipients, text); err != nil {
		m.conversation.notice = err.Error()
		return m, nil
	}
	m.conversation.composer = ""
	m.conversation.notice = ""
	m.refreshProjection()
	return m, nil
}

func hasMention(text string) bool {
	for _, token := range strings.Fields(text) {
		token = strings.Trim(token, ".,:;!?()[]{}<>\"'")
		if strings.HasPrefix(token, "@") && len(token) > 1 {
			return true
		}
	}
	return false
}

func (m Model) executeCommand(command string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return m, nil
	}
	switch strings.ToLower(fields[0]) {
	case "ces":
		if len(fields) == 2 && strings.EqualFold(fields[1], "view") {
			m.returnScreen, m.screen = ConversationScreen, EpistemicScreen
			return m, nil
		}
		if len(fields) != 2 || (fields[1] != "on" && fields[1] != "off") {
			m.conversation.notice = "use :ces on, :ces off, or :ces view"
			return m, nil
		}
		policy := run.CESPolicyOff
		if fields[1] == "on" {
			policy = run.CESPolicyOn
		}
		if err := m.controller.SetCESPolicy(policy); err != nil {
			m.conversation.notice = err.Error()
		} else {
			m.refreshProjection()
			m.conversation.notice = ""
		}
	case "events":
		m.toggleEvents()
	case "memory":
		m.toggleMemory()
	case "recall":
		query := strings.TrimSpace(strings.Join(fields[1:], " "))
		if query == "" {
			m.conversation.notice = "use :recall <query>"
			break
		}
		agent := m.memoryCommandAgent()
		if agent == nil {
			m.conversation.notice = "select an agent or task first"
			break
		}
		result, err := m.controller.RecallFor(agent.DisplayName, query)
		if err != nil {
			m.conversation.notice = err.Error()
			break
		}
		m.conversation.lastMemoryQuery = query
		m.conversation.lastMemoryAgent = agent.ID
		m.conversation.lastMemoryTask = m.memoryCommandTask(agent.ID)
		m.conversation.lastMemoryResult = &result
		m.conversation.memoryDetailID = ""
		m.conversation.memoryDetailScroll = 0
		m.refreshProjection()
		m.openMemory()
	case "remember":
		text := strings.TrimSpace(strings.Join(fields[1:], " "))
		if text == "" {
			m.conversation.notice = "use :remember <text>"
			break
		}
		agent := m.memoryCommandAgent()
		if agent == nil {
			m.conversation.notice = "select an agent or task first"
			break
		}
		queued, err := m.controller.RememberFromHuman(agent.DisplayName, core.MemoryScopeWorkspace, core.ClaimInstruction, text, nil, nil)
		if err != nil {
			m.conversation.notice = err.Error()
			break
		}
		status := "not queued"
		if queued {
			status = "queued"
		}
		m.conversation.lastMemoryWrite = status + " · " + text
		m.conversation.lastMemoryAgent = agent.ID
		m.conversation.lastMemoryTask = m.memoryCommandTask(agent.ID)
		m.conversation.memoryDetailID = ""
		m.conversation.memoryDetailScroll = 0
		m.refreshProjection()
		m.openMemory()
	case "agent":
		if len(fields) < 2 {
			m.conversation.paletteOpen = true
			m.conversation.paletteFilter = "@"
			return m, nil
		}
		if err := m.chooseAgent(fields[1]); err != nil {
			m.conversation.notice = err.Error()
		} else {
			m.conversation.notice = ""
		}
	case "cancel":
		if len(fields) == 1 {
			if err := m.controller.Cancel(); err != nil {
				m.conversation.notice = err.Error()
			} else {
				m.refreshProjection()
				m.conversation.notice = "run cancelled"
			}
			break
		}
		if len(fields) != 2 {
			m.conversation.notice = "use :cancel or :cancel TASK-ID"
			break
		}
		task, err := m.taskByPrefix(fields[1])
		if err != nil {
			m.conversation.notice = err.Error()
			break
		}
		if err := m.controller.CancelTask(task); err != nil {
			m.conversation.notice = err.Error()
		} else {
			m.refreshProjection()
			m.conversation.notice = "task cancelled"
		}
	case "help":
		m.conversation.helpOpen = !m.conversation.helpOpen
	case "quit":
		return m, m.quitCmd()
	case "legacy":
		m.returnScreen = ConversationScreen
		m.screen = LegacyDashboardScreen
	default:
		m.conversation.notice = fmt.Sprintf("unknown command: %s", fields[0])
	}
	return m, nil
}

func (m Model) taskByPrefix(prefix string) (core.TaskID, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return "", fmt.Errorf("task ID prefix is required")
	}
	var match core.TaskID
	for _, task := range m.projection.Tasks {
		if strings.HasPrefix(strings.ToLower(string(task.ID)), prefix) {
			if match != "" {
				return "", fmt.Errorf("task ID prefix %q matches more than one task", prefix)
			}
			match = task.ID
		}
	}
	if match == "" {
		return "", fmt.Errorf("no task matches %q", prefix)
	}
	return match, nil
}

func (m *Model) toggleCES() {
	policy := run.CESPolicyOn
	if m.runStatus.Requested == run.CESPolicyOn {
		policy = run.CESPolicyOff
	}
	if err := m.controller.SetCESPolicy(policy); err != nil {
		m.conversation.notice = err.Error()
		return
	}
	m.refreshProjection()
	m.conversation.notice = ""
}

func (m Model) updateEvents(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.ToggleCES):
		m.toggleCES()
	case len(msg.Text) == 1 && msg.Text[0] >= '0' && msg.Text[0] <= '9':
		m.chooseTab(int(msg.Text[0] - '0'))
	case key.Matches(msg, m.keys.CycleCategory):
		m.conversation.eventCategory = (m.conversation.eventCategory + 1) % (len(eventCategories) + 1)
		m.refreshFilteredEvents()
	case key.Matches(msg, m.keys.Up):
		m.conversation.eventFollow = false
		m.conversation.eventSelected = max(0, m.conversation.eventSelected-1)
	case key.Matches(msg, m.keys.Down):
		m.conversation.eventFollow = false
		m.conversation.eventSelected = min(len(m.conversation.filteredEvents)-1, m.conversation.eventSelected+1)
	case key.Matches(msg, m.keys.PageUp):
		m.conversation.eventFollow = false
		m.conversation.eventSelected = max(0, m.conversation.eventSelected-max(1, m.height/2))
	case key.Matches(msg, m.keys.PageDown):
		m.conversation.eventFollow = false
		m.conversation.eventSelected = min(len(m.conversation.filteredEvents)-1, m.conversation.eventSelected+max(1, m.height/2))
	case key.Matches(msg, m.keys.Enter):
		if index := m.conversation.eventSelected; index >= 0 && index < len(m.conversation.filteredEvents) {
			item := m.conversation.filteredEvents[index]
			if m.conversation.expandedEvent == item.ID {
				m.conversation.expandedEvent = ""
			} else {
				m.conversation.expandedEvent = item.ID
			}
		}
	case key.Matches(msg, m.keys.ToggleFollow):
		m.conversation.eventFollow = !m.conversation.eventFollow
		if m.conversation.eventFollow {
			m.conversation.eventSelected = len(m.conversation.filteredEvents) - 1
		}
	case key.Matches(msg, m.keys.ToggleAgent):
		m.conversation.eventAgentOn = !m.conversation.eventAgentOn
		m.refreshFilteredEvents()
	case msg.Text == "/":
		m.conversation.searchMode = true
		m.conversation.eventSearch = ""
		m.refreshFilteredEvents()
	}
	return m, nil
}

func (m Model) updateEventSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Escape), key.Matches(msg, m.keys.Enter):
		m.conversation.searchMode = false
	case msg.Code == tea.KeyBackspace || msg.Code == tea.KeyDelete:
		if len(m.conversation.eventSearch) > 0 {
			m.conversation.eventSearch = m.conversation.eventSearch[:len(m.conversation.eventSearch)-1]
			m.refreshFilteredEvents()
		}
	case msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta) == 0:
		m.conversation.eventSearch += msg.Text
		m.refreshFilteredEvents()
	}
	return m, nil
}

func (m Model) paletteItems() []string {
	items := []string{":ces on", ":ces off", ":ces view", ":events", ":memory", ":recall QUERY", ":remember TEXT", ":agent", ":cancel", ":cancel TASK-ID", ":help", ":legacy", ":quit"}
	for _, agent := range m.projection.Agents {
		items = append(items, "@"+agent.DisplayName)
	}
	return items
}

func (m Model) updatePalette(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	items := filterPalette(m.paletteItems(), m.conversation.paletteFilter)
	switch {
	case key.Matches(msg, m.keys.Escape):
		m.conversation.paletteOpen = false
	case key.Matches(msg, m.keys.Up):
		m.conversation.paletteSelected = max(0, m.conversation.paletteSelected-1)
	case key.Matches(msg, m.keys.Down):
		m.conversation.paletteSelected = min(len(items)-1, m.conversation.paletteSelected+1)
	case key.Matches(msg, m.keys.Enter):
		if len(items) > 0 && m.conversation.paletteSelected < len(items) {
			choice := items[m.conversation.paletteSelected]
			m.conversation.paletteOpen = false
			if strings.HasPrefix(choice, "@") {
				if err := m.chooseAgent(strings.TrimPrefix(choice, "@")); err != nil {
					m.conversation.notice = err.Error()
				}
				return m, nil
			}
			return m.executeCommand(strings.TrimPrefix(choice, ":"))
		}
	case msg.Code == tea.KeyBackspace || msg.Code == tea.KeyDelete:
		if len(m.conversation.paletteFilter) > 0 {
			m.conversation.paletteFilter = m.conversation.paletteFilter[:len(m.conversation.paletteFilter)-1]
		}
		m.conversation.paletteSelected = 0
	case msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta) == 0:
		m.conversation.paletteFilter += msg.Text
		m.conversation.paletteSelected = 0
	}
	return m, nil
}
