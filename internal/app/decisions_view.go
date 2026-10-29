package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/core"
)

type decisionHistory struct {
	id     core.EventID
	kind   core.DecisionKind
	status string
	action string
	events []core.Event
}

func decisionEventType(eventType string) bool {
	switch eventType {
	case "decision.requested", "decision.recommended", "decision.unavailable", "decision.budget_exhausted", "decision.applied", "decision.overridden", "decision.ignored":
		return true
	default:
		return false
	}
}

func decisionRequestID(item core.Event) core.EventID {
	if item.CorrelationID != "" {
		return item.CorrelationID
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(item.Payload, &payload) == nil {
		for _, key := range []string{"id", "request_id"} {
			var id core.EventID
			if json.Unmarshal(payload[key], &id) == nil && id != "" {
				return id
			}
		}
	}
	return item.ID
}

func (m Model) decisionHistory() []decisionHistory {
	byID := map[core.EventID]*decisionHistory{}
	var order []core.EventID
	for _, item := range m.projection.Events {
		if !decisionEventType(item.Type) {
			continue
		}
		id := decisionRequestID(item)
		group := byID[id]
		if group == nil {
			group = &decisionHistory{id: id}
			byID[id] = group
			order = append(order, id)
		}
		group.events = append(group.events, item)
		var payload map[string]any
		_ = json.Unmarshal(item.Payload, &payload)
		switch item.Type {
		case "decision.requested":
			group.status = "requested"
			group.kind = core.DecisionKind(value(payload, "kind"))
		case "decision.recommended":
			group.status = "recommended"
			group.action = value(payload, "action")
		case "decision.unavailable", "decision.budget_exhausted":
			group.status = strings.TrimPrefix(item.Type, "decision.")
		case "decision.applied", "decision.overridden", "decision.ignored":
			group.status = strings.TrimPrefix(item.Type, "decision.")
			group.action = value(payload, "action")
		}
	}
	groups := make([]decisionHistory, 0, len(order))
	for _, id := range order {
		groups = append(groups, *byID[id])
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].events[0].Time.Before(groups[j].events[0].Time)
	})
	return groups
}

func (m Model) updateDecisions(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	groups := m.decisionHistory()
	detailOpen := m.conversation.decisionDetailID != ""
	switch {
	case key.Matches(msg, m.keys.Up):
		if detailOpen {
			m.conversation.decisionDetailScroll = max(0, m.conversation.decisionDetailScroll-1)
		} else {
			m.conversation.decisionSelected = max(0, m.conversation.decisionSelected-1)
		}
	case key.Matches(msg, m.keys.Down):
		if detailOpen {
			m.conversation.decisionDetailScroll++
		} else {
			m.conversation.decisionSelected = min(max(0, len(groups)-1), m.conversation.decisionSelected+1)
		}
	case key.Matches(msg, m.keys.PageUp):
		if detailOpen {
			m.conversation.decisionDetailScroll = max(0, m.conversation.decisionDetailScroll-max(1, m.height/2))
		} else {
			m.conversation.decisionSelected = max(0, m.conversation.decisionSelected-max(1, m.height/2))
		}
	case key.Matches(msg, m.keys.PageDown):
		if detailOpen {
			m.conversation.decisionDetailScroll += max(1, m.height/2)
		} else {
			m.conversation.decisionSelected = min(max(0, len(groups)-1), m.conversation.decisionSelected+max(1, m.height/2))
		}
	case key.Matches(msg, m.keys.Enter) && detailOpen:
		m.conversation.decisionDetailID = ""
		m.conversation.decisionDetailScroll = 0
	case key.Matches(msg, m.keys.Enter) && len(groups) > 0:
		index := min(max(0, m.conversation.decisionSelected), len(groups)-1)
		m.conversation.decisionDetailID = groups[index].id
		m.conversation.decisionDetailScroll = 0
	}
	return m, nil
}

func (m Model) decisionLines(height, width int) []string {
	if height <= 0 || width <= 0 {
		return nil
	}
	groups := m.decisionHistory()
	lines := []string{"DECISIONS · grouped by request ID", "Decision service " + availabilityLabel(m.runStatus.DecisionAvailable)}
	if m.conversation.decisionDetailID != "" {
		lines = append(lines, decisionDetailLines(groups, m.conversation.decisionDetailID)...)
		wrapped := wrapMemoryLines(lines, width)
		start := min(max(0, m.conversation.decisionDetailScroll), max(0, len(wrapped)-height))
		return truncateMemoryLines(wrapped[start:min(len(wrapped), start+height)], height, width)
	}
	if len(groups) == 0 {
		lines = append(lines, "No decision requests recorded")
		return truncateMemoryLines(lines, height, width)
	}
	selected := min(max(0, m.conversation.decisionSelected), len(groups)-1)
	start := max(0, selected-3)
	end := min(len(groups), start+max(1, min(10, height-len(lines))))
	for i := start; i < end; i++ {
		marker := "  "
		if i == selected && m.conversation.focus == focusDecisions {
			marker = "> "
		}
		lines = append(lines, marker+decisionSummaryLine(groups[i]))
	}
	return truncateMemoryLines(lines, height, width)
}

func decisionSummaryLine(group decisionHistory) string {
	parts := []string{strings.ToUpper(strings.ReplaceAll(string(group.kind), "_", " "))}
	if group.action != "" {
		parts = append(parts, group.action)
	}
	parts = append(parts, group.status, string(group.id))
	return strings.Join(parts, " · ")
}

func decisionDetailLines(groups []decisionHistory, id core.EventID) []string {
	var group *decisionHistory
	for i := range groups {
		if groups[i].id == id {
			group = &groups[i]
			break
		}
	}
	if group == nil {
		return []string{"Decision request is not in the current event snapshot."}
	}
	lines := []string{"DECISION " + string(group.id), "Kind: " + string(group.kind), "Status: " + group.status, "SELECTED REQUEST · arrows or Page Up/Down scroll"}
	for _, item := range group.events {
		switch item.Type {
		case "decision.requested":
			lines = append(lines, "Request inputs · decision.requested")
			lines = append(lines, prettyDecisionPayload(item.Payload)...)
		case "decision.recommended", "decision.unavailable", "decision.budget_exhausted":
			lines = append(lines, "Decision outputs · "+item.Type)
			lines = append(lines, prettyDecisionPayload(item.Payload)...)
		case "decision.applied", "decision.overridden", "decision.ignored":
			lines = append(lines, "Execution records · "+item.Type)
			lines = append(lines, prettyDecisionPayload(item.Payload)...)
		}
	}
	return lines
}

func prettyDecisionPayload(payload json.RawMessage) []string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, payload, "", "  "); err != nil {
		return []string{fmt.Sprintf("  %s", payload)}
	}
	var lines []string
	for _, line := range strings.Split(pretty.String(), "\n") {
		lines = append(lines, "  "+line)
	}
	return lines
}
