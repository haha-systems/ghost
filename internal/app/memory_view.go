package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/run"
)

type memoryEventPayload struct {
	Reason           string              `json:"reason"`
	LatencyMS        int64               `json:"latency_ms"`
	Tokens           int                 `json:"tokens"`
	InjectedTokens   int                 `json:"injected_tokens"`
	Truncated        bool                `json:"truncated"`
	Records          []core.MemoryRecord `json:"records"`
	RecordIDs        []string            `json:"record_ids"`
	InjectedIDs      []string            `json:"injected_ids"`
	MemoryIDs        []string            `json:"memory_ids"`
	Summary          string              `json:"summary"`
	Scope            string              `json:"scope"`
	ErrorClass       string              `json:"error_class"`
	WriteID          string              `json:"write_id"`
	Count            int                 `json:"count"`
	TruncatedRecords int                 `json:"truncated_records"`
}

func (m Model) memoryHistory() (run.MemoryHistory, string) {
	if task := m.selectedMemoryTask(); task != nil {
		return m.projection.MemoryByTask[task.ID], "task " + shortTaskID(string(task.ID))
	}
	if agent := m.selectedAgent(); agent != nil {
		return m.projection.MemoryByAgent[agent.ID], "agent " + agent.DisplayName
	}
	return m.projection.Memory, "workspace"
}

func (m Model) workspaceMemoryView() bool {
	return m.selectedMemoryTask() == nil && m.selectedAgent() == nil
}

func (m Model) memoryStatus() string {
	if !m.config.Memory.Enabled {
		return "Memory off (not configured)"
	}
	for i := len(m.projection.Memory.Events) - 1; i >= 0; i-- {
		item := m.projection.Memory.Events[i]
		var payload memoryEventPayload
		_ = json.Unmarshal(item.Payload, &payload)
		switch item.Type {
		case core.MemoryEventUnavailable:
			if payload.Reason == "" {
				payload.Reason = "reason not reported"
			}
			return "Memory unavailable: " + memoryText(payload.Reason)
		case core.MemoryEventRetrievalFailed:
			if payload.ErrorClass == "" {
				payload.ErrorClass = "reason not reported"
			}
			return "Memory retrieval failed: " + memoryText(payload.ErrorClass)
		}
	}
	return "Memory enabled; availability not reported"
}

func (m Model) memoryCommandAgent() *core.Agent {
	if task := m.selectedMemoryTask(); task != nil {
		for i := range m.projection.Agents {
			if m.projection.Agents[i].ID == task.Owner {
				return &m.projection.Agents[i]
			}
		}
		return nil
	}
	return m.selectedAgent()
}

func (m Model) memoryCommandTask(agent core.AgentID) core.TaskID {
	if task := m.selectedMemoryTask(); task != nil {
		return task.ID
	}
	for i := len(m.projection.Tasks) - 1; i >= 0; i-- {
		task := m.projection.Tasks[i]
		if task.Owner == agent && !task.Status.IsTerminal() {
			return task.ID
		}
	}
	return ""
}

func (m Model) memoryRecords(history run.MemoryHistory) ([]core.MemoryRecord, int64, bool) {
	var records []core.MemoryRecord
	latency := int64(-1)
	var truncated bool
	var ids []string
	for _, item := range history.Events {
		if item.Type != core.MemoryEventRetrievalCompleted {
			continue
		}
		var payload memoryEventPayload
		_ = json.Unmarshal(item.Payload, &payload)
		records, latency, truncated = payload.Records, payload.LatencyMS, payload.Truncated
		ids = payload.RecordIDs
		if len(ids) == 0 {
			ids = payload.InjectedIDs
		}
	}
	if m.lastRecallVisible() && m.conversation.lastMemoryResult != nil {
		result := m.conversation.lastMemoryResult
		records, truncated = result.Records, result.Truncated
		ids = recordIDsForMemoryRecords(records)
	}
	workspaceOnly := m.workspaceMemoryView()
	if workspaceOnly {
		visible := records[:0]
		for _, item := range records {
			if item.Scope == core.MemoryScopeWorkspace || (m.config.Memory.ShareGlobal && item.Scope == core.MemoryScopeGlobal) {
				visible = append(visible, item)
			}
		}
		records = visible
	}
	if len(records) == 0 && len(ids) > 0 && !workspaceOnly {
		records = make([]core.MemoryRecord, 0, len(ids))
		for _, id := range ids {
			records = append(records, core.MemoryRecord{ID: id, Text: "Record details were not stored in this history."})
		}
	}
	return records, latency, truncated
}

func (m Model) lastRecallVisible() bool {
	if m.conversation.lastMemoryResult == nil {
		return false
	}
	if task := m.selectedMemoryTask(); task != nil {
		return m.conversation.lastMemoryTask == task.ID
	}
	if agent := m.selectedAgent(); agent != nil {
		return m.conversation.lastMemoryAgent == agent.ID
	}
	return false
}

func recordIDsForMemoryRecords(records []core.MemoryRecord) []string {
	ids := make([]string, 0, len(records))
	for _, item := range records {
		ids = append(ids, item.ID)
	}
	return ids
}

func (m Model) updateMemory(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	history, _ := m.memoryHistory()
	records, _, _ := m.memoryRecords(history)
	detailOpen := m.conversation.memoryDetailID != ""
	switch {
	case len(msg.Text) == 1 && msg.Text[0] >= '0' && msg.Text[0] <= '9':
		m.chooseTab(int(msg.Text[0] - '0'))
		m.conversation.memorySelected = 0
		m.conversation.memoryDetailID = ""
		m.conversation.memoryDetailScroll = 0
	case key.Matches(msg, m.keys.Up):
		if detailOpen {
			m.conversation.memoryDetailScroll = max(0, m.conversation.memoryDetailScroll-1)
		} else {
			m.conversation.memorySelected = max(0, m.conversation.memorySelected-1)
		}
	case key.Matches(msg, m.keys.Down):
		if detailOpen {
			m.conversation.memoryDetailScroll++
		} else {
			m.conversation.memorySelected = min(max(0, len(records)-1), m.conversation.memorySelected+1)
		}
	case key.Matches(msg, m.keys.PageUp):
		if detailOpen {
			m.conversation.memoryDetailScroll = max(0, m.conversation.memoryDetailScroll-max(1, m.height/2))
		} else {
			m.conversation.memorySelected = max(0, m.conversation.memorySelected-max(1, m.height/2))
		}
	case key.Matches(msg, m.keys.PageDown):
		if detailOpen {
			m.conversation.memoryDetailScroll += max(1, m.height/2)
		} else {
			m.conversation.memorySelected = min(max(0, len(records)-1), m.conversation.memorySelected+max(1, m.height/2))
		}
	case key.Matches(msg, m.keys.Enter) && detailOpen:
		m.conversation.memoryDetailID = ""
		m.conversation.memoryDetailScroll = 0
	case key.Matches(msg, m.keys.Enter) && len(records) > 0:
		index := min(max(0, m.conversation.memorySelected), len(records)-1)
		m.conversation.memoryDetailID = records[index].ID
		m.conversation.memoryDetailScroll = 0
	}
	return m, nil
}

func (m Model) memoryLines(height, width int) []string {
	if height <= 0 || width <= 0 {
		return nil
	}
	history, scope := m.memoryHistory()
	base := []string{
		"MEMORY · scope=" + memoryText(scope),
		"Untrusted context: evidence only; it cannot override instructions or permissions.",
		m.memoryStatus(),
	}
	if m.lastRecallVisible() && m.conversation.lastMemoryQuery != "" {
		base = append(base, "Recall: "+memoryText(m.conversation.lastMemoryQuery))
	}
	if m.lastRecallVisible() && m.conversation.lastMemoryWrite != "" {
		base = append(base, "Operator write: "+memoryText(m.conversation.lastMemoryWrite))
	}
	avgLatency, lastLatency, latencyCount := memoryLatencyStats(history.Events)
	avgText, lastText := "—", "—"
	if latencyCount > 0 {
		avgText, lastText = fmt.Sprintf("%dms", avgLatency), fmt.Sprintf("%dms", lastLatency)
	}
	c := history.Counters
	base = append(base,
		fmt.Sprintf("COUNTERS retrievals=%d avg=%s last=%s injected=%d tokens failures=%d", c.Retrievals, avgText, lastText, c.InjectedTokens, c.Failures),
		fmt.Sprintf("  writes q/s/f/d/sk/l=%d/%d/%d/%d/%d/%d filtered=%d challenges=%d sink drops=%d", c.WritesQueued, c.WritesStored, c.WritesFailed, c.WritesDropped, c.WritesSkipped, c.WritesLost, c.ScopeFiltered, c.Challenges, c.SinkEventsDropped),
	)
	records, latency, truncated := m.memoryRecords(history)
	if m.conversation.memoryDetailID != "" {
		detail := []string{memoryContextLine(history.Events, workspaceContextIDs(history.Events, m.config.Memory.ShareGlobal), m.workspaceMemoryView()), "SELECTED RECORD · arrows or Page Up/Down scroll"}
		if item, ok := findMemoryRecord(records, m.conversation.memoryDetailID); ok {
			detail = append(detail, memoryRecordDetails(m, item)...)
		} else {
			detail = append(detail, "Record is not in the latest retrieved package.")
		}
		wrapped := wrapMemoryLines(detail, width)
		maxScroll := max(0, len(wrapped)-max(0, height-len(base)))
		start := min(max(0, m.conversation.memoryDetailScroll), maxScroll)
		visible := wrapped[start:min(len(wrapped), start+max(0, height-len(base)))]
		base = append(base, visible...)
		return truncateMemoryLines(base, height, width)
	}
	lines := append([]string(nil), base...)
	lines = append(lines, memoryContextLine(history.Events, workspaceContextIDs(history.Events, m.config.Memory.ShareGlobal), m.workspaceMemoryView()))
	head := fmt.Sprintf("RECENTLY RETRIEVED · %d records", len(records))
	if latency >= 0 && len(records) > 0 {
		head += fmt.Sprintf(" · %dms", latency)
	}
	if truncated {
		head += " · truncated"
	}
	lines = append(lines, head)
	if len(records) == 0 {
		if m.lastRecallVisible() && m.conversation.lastMemoryResult != nil {
			lines = append(lines, "No matching memories")
		} else {
			lines = append(lines, "No retrieved memories")
		}
	} else {
		selected := min(max(0, m.conversation.memorySelected), len(records)-1)
		start := max(0, selected-3)
		end := min(len(records), start+8)
		for i := start; i < end; i++ {
			marker := "  "
			if i == selected && m.conversation.focus == focusMemory {
				marker = "> "
			}
			lines = append(lines, marker+memoryRecordSummary(records[i]))
		}
	}
	lines = append(lines, memoryWriteLines(history.Events, m.workspaceMemoryView(), m.config.Memory.ShareGlobal)...)
	return truncateMemoryLines(lines, height, width)
}

func wrapMemoryLines(lines []string, width int) []string {
	var wrapped []string
	for _, line := range lines {
		line = memoryText(line)
		parts := strings.Split(ansi.Hardwrap(line, max(1, width), false), "\n")
		wrapped = append(wrapped, parts...)
	}
	return wrapped
}

func truncateMemoryLines(lines []string, height, width int) []string {
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return lines[:min(height, len(lines))]
}

func memoryLatencyStats(events []core.Event) (avg, last int64, count int) {
	var total int64
	for _, item := range events {
		if item.Type != core.MemoryEventRetrievalCompleted && item.Type != core.MemoryEventRetrievalFailed {
			continue
		}
		var payload memoryEventPayload
		_ = json.Unmarshal(item.Payload, &payload)
		total += payload.LatencyMS
		last = payload.LatencyMS
		count++
	}
	if count > 0 {
		avg = total / int64(count)
	}
	return avg, last, count
}

func workspaceContextIDs(events []core.Event, shareGlobal bool) map[string]bool {
	ids := map[string]bool{}
	for _, item := range events {
		if item.Type != core.MemoryEventRetrievalCompleted {
			continue
		}
		var payload memoryEventPayload
		_ = json.Unmarshal(item.Payload, &payload)
		for _, record := range payload.Records {
			if record.Scope == core.MemoryScopeWorkspace || (shareGlobal && record.Scope == core.MemoryScopeGlobal) {
				ids[record.ID] = true
			}
		}
	}
	return ids
}

func memoryContextLine(events []core.Event, visibleIDs map[string]bool, filterIDs bool) string {
	var ids []string
	var tokens int
	var truncated bool
	found := false
	for _, item := range events {
		if item.Type != core.MemoryEventContextInjected {
			continue
		}
		var payload memoryEventPayload
		_ = json.Unmarshal(item.Payload, &payload)
		ids = payload.MemoryIDs
		if len(ids) == 0 {
			ids = payload.InjectedIDs
		}
		tokens, truncated, found = payload.Tokens, payload.Truncated, true
	}
	if !found {
		return "IN CONTEXT · no recorded injection"
	}
	if filterIDs {
		filtered := ids[:0]
		for _, id := range ids {
			if visibleIDs[id] {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	truncation := "no"
	if truncated {
		truncation = "yes"
	}
	label := "ids="
	tokenLabel := "tokens"
	if filterIDs {
		label = "shared IDs="
		tokenLabel = "total tokens"
	}
	return fmt.Sprintf("IN CONTEXT · %s%s · %d %s · truncated=%s", label, valueOr(memoryText(strings.Join(ids, ",")), "none"), tokens, tokenLabel, truncation)
}

func memoryWriteLines(events []core.Event, workspaceOnly, shareGlobal bool) []string {
	var writes []string
	for _, item := range events {
		outcome := ""
		switch item.Type {
		case core.MemoryEventWriteQueued:
			outcome = "queued"
		case core.MemoryEventWriteStored:
			outcome = "stored"
		case core.MemoryEventWriteFailed:
			outcome = "failed"
		case core.MemoryEventWriteDropped:
			outcome = "dropped"
		case core.MemoryEventWriteSkipped:
			outcome = "skipped"
		case core.MemoryEventWriteLost:
			outcome = "lost"
		default:
			continue
		}
		var payload memoryEventPayload
		_ = json.Unmarshal(item.Payload, &payload)
		if workspaceOnly && payload.Scope != string(core.MemoryScopeWorkspace) && !(shareGlobal && payload.Scope == string(core.MemoryScopeGlobal)) {
			continue
		}
		detail := memoryText(payload.Summary)
		if reason := payload.Reason; reason != "" {
			if detail != "" {
				detail += " · "
			}
			detail += memoryText(reason)
		} else if payload.ErrorClass != "" {
			detail += " · " + memoryText(payload.ErrorClass)
		}
		writes = append(writes, fmt.Sprintf("%s · %s", outcome, valueOr(detail, payload.WriteID)))
	}
	if len(writes) > 6 {
		writes = writes[len(writes)-6:]
	}
	if len(writes) == 0 {
		return []string{"NEW WRITES · none"}
	}
	out := []string{"NEW WRITES"}
	for i := len(writes) - 1; i >= 0; i-- {
		out = append(out, "  "+writes[i])
	}
	return out
}

func memoryRecordSummary(item core.MemoryRecord) string {
	confidence := item.Provenance.Confidence
	confidenceText := fmt.Sprintf("c=%.2f", confidence)
	if item.Foreign {
		confidenceText = fmt.Sprintf("reported=%.2f", item.ReportedConfidence)
	}
	flags := make([]string, 0, 4)
	if item.Provenance.MissingEvidence || len(memoryEvidence(item.Provenance)) == 0 {
		flags = append(flags, "evidence=MISSING")
	} else {
		flags = append(flags, fmt.Sprintf("evidence=%d", len(memoryEvidence(item.Provenance))))
	}
	if item.Foreign {
		flags = append(flags, "foreign")
	}
	if item.Provenance.Validation == core.ValidationDisputed || len(item.ConflictIDs) > 0 {
		flags = append(flags, "disputed")
	}
	if memorySuperseded(item) {
		flags = append(flags, "superseded")
	}
	parts := []string{memoryText(item.ID), memoryText(string(item.Scope)), memoryText(string(item.Provenance.ClaimKind)) + "/" + memoryText(string(item.Provenance.Validation)), confidenceText}
	parts = append(parts, flags...)
	return strings.Join(parts, " · ")
}

func memoryRecordDetails(m Model, item core.MemoryRecord) []string {
	confidence := fmt.Sprintf("%.2f", item.Provenance.Confidence)
	if item.Foreign {
		confidence = fmt.Sprintf("reported %.2f", item.ReportedConfidence)
	}
	backend := item.BackendState
	if backend == "" {
		backend = "not reported"
	}
	foreign := "no"
	if item.Foreign {
		foreign = "yes"
	}
	conflicts := strings.Join(item.ConflictIDs, ", ")
	if conflicts == "" && item.Provenance.Validation == core.ValidationDisputed {
		conflicts = "disputed; linked record not reported"
	}
	if conflicts == "" {
		conflicts = "none reported"
	}
	superseded := "no"
	if memorySuperseded(item) {
		superseded = "yes"
	}
	evidence := memoryEvidence(item.Provenance)
	evidenceText := "Evidence: MISSING"
	if len(evidence) > 0 {
		evidenceText = "Evidence: " + memoryText(strings.Join(evidence, ", "))
	}
	counter := strings.Join(item.Provenance.Counterevidence, ", ")
	if counter == "" {
		counter = "none"
	}
	events := strings.Join(item.Provenance.EventRefs, ", ")
	artifacts := strings.Join(item.Provenance.ArtifactRefs, ", ")
	if events == "" {
		events = "none"
	}
	if artifacts == "" {
		artifacts = "none"
	}
	return []string{
		"ID: " + memoryText(item.ID),
		fmt.Sprintf("Scope: %s · Agent: %s · Task: %s", memoryText(string(item.Scope)), valueOr(memoryText(m.agentLabel(item.Provenance.Agent)), "—"), valueOr(memoryText(string(item.Provenance.Task)), "—")),
		fmt.Sprintf("Claim: %s · Validation: %s · Confidence: %s", memoryText(string(item.Provenance.ClaimKind)), memoryText(string(item.Provenance.Validation)), confidence),
		"Backend state: " + memoryText(backend) + " · Foreign: " + foreign,
		evidenceText,
		"Source references: events=" + memoryText(events) + " · artifacts=" + memoryText(artifacts),
		"Counterevidence: " + memoryText(counter),
		"Conflicts: " + memoryText(conflicts) + " · Superseded: " + superseded,
		"Text: " + memoryText(item.Text),
	}
}

func memoryEvidence(provenance core.MemoryProvenance) []string {
	refs := append([]string(nil), provenance.Evidence...)
	seen := map[string]bool{}
	for _, ref := range refs {
		seen[ref] = true
	}
	for _, ref := range append(append([]string(nil), provenance.EventRefs...), provenance.ArtifactRefs...) {
		if ref != "" && !seen[ref] {
			refs = append(refs, ref)
			seen[ref] = true
		}
	}
	return refs
}

func memorySuperseded(item core.MemoryRecord) bool {
	return item.Provenance.Validation == core.ValidationSuperseded || item.BackendState == "deprecated" || item.BackendState == "revised"
}

func memoryText(value string) string {
	var clean strings.Builder
	clean.Grow(len(value))
	for _, r := range value {
		if unicode.IsControl(r) {
			if unicode.IsSpace(r) {
				clean.WriteByte(' ')
			}
			continue
		}
		clean.WriteRune(r)
	}
	return strings.Join(strings.Fields(clean.String()), " ")
}

func findMemoryRecord(records []core.MemoryRecord, id string) (core.MemoryRecord, bool) {
	for _, item := range records {
		if item.ID == id {
			return item, true
		}
	}
	return core.MemoryRecord{}, false
}
