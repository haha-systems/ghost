package run

import (
	"sort"
	"strings"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
)

func restoreConfiguredAgentIDs(configured config.AgentSet, order []string, stored map[string]core.AgentID) map[string]core.AgentID {
	names := config.OrderedAgentNames(configured, order)
	resolved := map[string]core.AgentID{}
	used := map[core.AgentID]bool{}
	for _, name := range names {
		for storedName, id := range stored {
			if strings.HasPrefix(storedName, "\x00agent-id:") || !strings.EqualFold(storedName, name) || id.Validate() != nil || used[id] {
				continue
			}
			resolved[name] = id
			used[id] = true
			break
		}
	}
	legacy := make([]core.AgentID, 0)
	legacySeen := map[core.AgentID]bool{}
	for _, id := range stored {
		if id.Validate() == nil && !used[id] && !legacySeen[id] {
			legacy = append(legacy, id)
			legacySeen[id] = true
		}
	}
	sort.Slice(legacy, func(i, j int) bool { return legacy[i] < legacy[j] })
	for _, name := range names {
		if resolved[name] != "" || len(legacy) == 0 {
			continue
		}
		resolved[name] = legacy[0]
		legacy = legacy[1:]
	}
	return resolved
}

func (o *owner) recordWorkspaceIdentity(eventType string) {
	if o.workspace == nil {
		return
	}
	type agentIdentity struct {
		Name string       `json:"name"`
		ID   core.AgentID `json:"id"`
	}
	payload := struct {
		Agents []agentIdentity `json:"agents"`
	}{}
	for _, name := range o.workspace.agentNamesSnapshot() {
		if id, ok := o.workspace.agentID(name); ok {
			payload.Agents = append(payload.Agents, agentIdentity{Name: name, ID: id})
		}
	}
	item, err := newCoreEvent(o.eventContext(), timeNow(), core.Actor{Kind: core.ActorSystem, ID: "controller"}, "controller", eventType, "", "", payload)
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.publishCoreEvent(item)
}

func (o *owner) recordRecoveryWarning(warning string) {
	item, err := newCoreEvent(o.eventContext(), timeNow(), core.Actor{Kind: core.ActorSystem, ID: "controller"}, "controller", "workspace.recovery_warning", "", "", map[string]any{"warning": warning})
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.publishCoreEvent(item)
}

func (o *owner) recordRecoverySummary() {
	item, err := newCoreEvent(o.eventContext(), timeNow(), core.Actor{Kind: core.ActorSystem, ID: "controller"}, "controller", "workspace.recovered", "", "", o.recovery)
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.publishCoreEvent(item)
}
