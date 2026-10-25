package run

import (
	"encoding/json"
	"sort"

	"github.com/haha-systems/ghost/internal/core"
)

func (o *owner) recordLostMemoryWrites(events []core.Event) {
	queued := map[string]core.Event{}
	terminal := map[string]bool{}
	for _, item := range events {
		if item.Category != core.CategoryMemory {
			continue
		}
		var payload struct {
			WriteID string `json:"write_id"`
		}
		if json.Unmarshal(item.Payload, &payload) != nil || payload.WriteID == "" {
			continue
		}
		switch item.Type {
		case core.MemoryEventWriteQueued:
			queued[payload.WriteID] = item
		case core.MemoryEventWriteStored, core.MemoryEventWriteFailed, core.MemoryEventWriteDropped,
			core.MemoryEventWriteSkipped, core.MemoryEventWriteLost:
			terminal[payload.WriteID] = true
		}
	}
	ids := make([]string, 0, len(queued))
	for id := range queued {
		if !terminal[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		prior := queued[id]
		var payload map[string]any
		if json.Unmarshal(prior.Payload, &payload) != nil {
			payload = map[string]any{"write_id": id}
		}
		payload["reason"] = "ghost restarted before write completed"
		ctx := o.eventContext()
		ctx.TaskID, ctx.ThreadID, ctx.CorrelationID = prior.TaskID, prior.ThreadID, prior.CorrelationID
		item, err := newCoreEvent(ctx, timeNow(), core.Actor{Kind: core.ActorSystem, ID: "memory-recovery"}, "memory", core.MemoryEventWriteLost, prior.AgentID, "", payload)
		if err != nil {
			o.coreProjectionError(err)
			continue
		}
		o.publishCoreEvent(item)
	}
}
