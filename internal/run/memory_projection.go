package run

import (
	"encoding/json"

	"github.com/haha-systems/ghost/internal/core"
)

// MemoryCounters summarizes memory activity from persisted workspace events.
type MemoryCounters struct {
	Events             int   `json:"events"`
	Retrievals         int   `json:"retrievals"`
	RetrievalLatencyMS int64 `json:"retrieval_latency_ms"`
	InjectedTokens     int   `json:"injected_tokens"`
	Failures           int   `json:"failures"`
	WritesQueued       int   `json:"writes_queued"`
	WritesStored       int   `json:"writes_stored"`
	WritesFailed       int   `json:"writes_failed"`
	WritesDropped      int   `json:"writes_dropped"`
	WritesSkipped      int   `json:"writes_skipped"`
	WritesLost         int   `json:"writes_lost"`
	ScopeFiltered      int   `json:"scope_filtered"`
	Challenges         int   `json:"challenges"`
	SinkEventsDropped  int   `json:"sink_events_dropped"`
}

// MemoryHistory keeps memory events and counters for one task or agent.
type MemoryHistory struct {
	Counters MemoryCounters `json:"counters"`
	Events   []core.Event   `json:"events,omitempty"`
}

func addMemoryEvent(history *MemoryHistory, item core.Event) {
	history.Counters.Events++
	history.Events = append(history.Events, cloneCoreEvent(item))
	var payload struct {
		LatencyMS int64 `json:"latency_ms"`
		Tokens    int   `json:"tokens"`
		Count     int   `json:"count"`
	}
	_ = json.Unmarshal(item.Payload, &payload)
	switch item.Type {
	case core.MemoryEventRetrievalStarted:
		history.Counters.Retrievals++
	case core.MemoryEventRetrievalCompleted, core.MemoryEventRetrievalFailed:
		history.Counters.RetrievalLatencyMS += payload.LatencyMS
		if item.Type == core.MemoryEventRetrievalFailed {
			history.Counters.Failures++
		}
	case core.MemoryEventContextInjected:
		history.Counters.InjectedTokens += payload.Tokens
	case core.MemoryEventWriteQueued:
		history.Counters.WritesQueued++
	case core.MemoryEventWriteStored:
		history.Counters.WritesStored++
	case core.MemoryEventWriteFailed:
		history.Counters.WritesFailed++
		history.Counters.Failures++
	case core.MemoryEventWriteDropped:
		history.Counters.WritesDropped++
	case core.MemoryEventWriteSkipped:
		history.Counters.WritesSkipped++
	case core.MemoryEventWriteLost:
		history.Counters.WritesLost++
		history.Counters.Failures++
	case core.MemoryEventScopeFiltered:
		history.Counters.ScopeFiltered++
	case core.MemoryEventChallengeRecorded:
		history.Counters.Challenges++
	case core.MemoryEventSinkDropped:
		history.Counters.SinkEventsDropped += payload.Count
	}
}

func memoryCountersForEvents(events []core.Event) MemoryCounters {
	var history MemoryHistory
	for _, item := range events {
		if item.Category == core.CategoryMemory {
			addMemoryEvent(&history, item)
		}
	}
	return history.Counters
}

func (p *workspaceProjection) projectMemoryEventLocked(item core.Event) {
	if item.Category != core.CategoryMemory {
		return
	}
	addMemoryEvent(&p.memory, item)
	if item.TaskID != "" {
		if p.memoryByTask == nil {
			p.memoryByTask = map[core.TaskID]MemoryHistory{}
		}
		history := p.memoryByTask[item.TaskID]
		addMemoryEvent(&history, item)
		p.memoryByTask[item.TaskID] = history
	}
	if item.AgentID != "" {
		if p.memoryByAgent == nil {
			p.memoryByAgent = map[core.AgentID]MemoryHistory{}
		}
		history := p.memoryByAgent[item.AgentID]
		addMemoryEvent(&history, item)
		p.memoryByAgent[item.AgentID] = history
	}
}

func cloneMemoryHistory(source MemoryHistory) MemoryHistory {
	out := MemoryHistory{Counters: source.Counters, Events: make([]core.Event, 0, len(source.Events))}
	for _, item := range source.Events {
		out.Events = append(out.Events, cloneCoreEvent(item))
	}
	return out
}
