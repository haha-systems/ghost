package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/memory"
	"github.com/haha-systems/ghost/internal/memory/ghostdive"
	"github.com/haha-systems/ghost/internal/runtime"
)

// MemoryService is the runtime boundary for bounded reads and queued writes.
type MemoryService interface {
	Recall(context.Context, core.MemoryRecallRequest) core.MemoryRecallResult
	Enqueue(core.MemoryWrite) bool
	Close(context.Context) (memory.CloseResult, error)
}

// MemoryFactory builds a service after Ghost has restored stable workspace
// and agent IDs. The event callback must not block.
type MemoryFactory func(core.WorkspaceID, map[string]core.AgentID, func(core.Event)) MemoryService

// MemoryFromConfig returns the step 2 Ghostdive constructor for enabled memory.
// Disabled memory stays nil and emits no events.
func MemoryFromConfig(settings config.MemoryConfig) MemoryFactory {
	if !settings.Enabled {
		return nil
	}
	return func(workspace core.WorkspaceID, agents map[string]core.AgentID, sink func(core.Event)) MemoryService {
		return ghostdive.NewFromConfig(settings, workspace, agents, memory.EventSink(sink))
	}
}

type memoryDelivery struct {
	agent   core.AgentID
	task    core.TaskID
	thread  core.ThreadID
	content string
}

func memoryScopes(global bool) []core.MemoryScope {
	scopes := []core.MemoryScope{core.MemoryScopeAgent, core.MemoryScopeTask, core.MemoryScopeWorkspace}
	if global {
		scopes = append(scopes, core.MemoryScopeGlobal)
	}
	return scopes
}

func memoryTaskParents(workspace *workspaceProjection, taskID core.TaskID) []core.TaskID {
	var parents []core.TaskID
	seen := map[core.TaskID]bool{}
	for taskID != "" && !seen[taskID] {
		seen[taskID] = true
		task, ok := workspace.task(taskID)
		if !ok || task.Parent == nil {
			break
		}
		parents = append(parents, *task.Parent)
		taskID = *task.Parent
	}
	return parents
}

func memoryContext(service MemoryService, ctx context.Context, requester core.AgentID, taskID core.TaskID, parents []core.TaskID, query, reason string, scopes []core.MemoryScope) core.MemoryRecallResult {
	if service == nil {
		return core.MemoryRecallResult{}
	}
	return service.Recall(ctx, core.MemoryRecallRequest{
		Query: query, Requester: requester, Task: taskID, ParentTasks: parents,
		Scopes: scopes, Reason: reason,
	})
}

func shortMemoryText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit])
	}
	return string(runes)
}

func (o *owner) memoryEvent(event core.Event) {
	select {
	case o.memoryEvents <- event:
	default:
		o.memorySinkDropped.Add(1)
	}
}

func (o *owner) flushMemorySinkDrops() {
	if o.memorySinkDropped.Load() == 0 || o.workspace == nil {
		return
	}
	count := o.memorySinkDropped.Swap(0)
	if count == 0 {
		return
	}
	item, err := newCoreEvent(o.eventContext(), timeNow(), core.Actor{Kind: core.ActorSystem, ID: "memory"}, "memory", core.MemoryEventSinkDropped, "", "", map[string]any{
		"count": count, "reason": "memory_event_queue_full",
	})
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.publishCoreEvent(item)
}

func (o *owner) drainMemoryEvents() {
	for {
		select {
		case item := <-o.memoryEvents:
			o.publishCoreEvent(item)
		default:
			o.flushMemorySinkDrops()
			return
		}
	}
}

func (o *owner) enqueueMemoryWrite(write core.MemoryWrite) bool {
	if o.memory == nil {
		return false
	}
	return o.memory.Enqueue(write)
}

func (o *owner) captureMemoryTask(task core.Task, messages []core.Message, events []core.Event) {
	if o.memory == nil || task.Status.IsTerminal() == false {
		return
	}
	if task.Status == core.TaskCancelled || task.FailureReason == "interrupted: ghost restarted" {
		return
	}
	provenance := core.MemoryProvenance{
		Agent: task.Owner, Task: task.ID, Workspace: string(o.workspace.workspaceID()),
		Validation: core.ValidationUnverified,
	}
	if task.Status == core.TaskCompleted {
		var result string
		var resultEvent core.EventID
		for _, message := range messages {
			if message.TaskID == nil || *message.TaskID != task.ID || message.Sender != task.Owner {
				continue
			}
			if message.Type != core.MessageTaskResult && message.Type != core.MessageReply {
				continue
			}
			result, resultEvent = message.Content, message.CausationID
		}
		if strings.TrimSpace(result) == "" {
			return
		}
		provenance.ClaimKind = core.ClaimInference
		provenance.Evidence = []string{"task:" + string(task.ID)}
		if resultEvent != "" {
			provenance.EventRefs = []string{string(resultEvent)}
		}
		scope := core.MemoryScopeWorkspace
		if task.Parent != nil {
			scope = core.MemoryScopeTask
		}
		o.enqueueMemoryWrite(core.MemoryWrite{
			Scope: scope, Subject: "task completed",
			Text: shortMemoryText(result, 4000), Provenance: provenance,
		})
		return
	}
	if task.Status == core.TaskFailed {
		provenance.ClaimKind = core.ClaimObservation
		for _, item := range events {
			if item.TaskID == task.ID && item.Type == "task.status_changed" {
				var payload struct {
					Status core.TaskStatus `json:"status"`
				}
				if json.Unmarshal(item.Payload, &payload) == nil && payload.Status == core.TaskFailed {
					provenance.EventRefs = []string{string(item.ID)}
				}
			}
		}
		reason := strings.TrimSpace(task.FailureReason)
		if reason == "" {
			reason = "unknown failure"
		}
		o.enqueueMemoryWrite(core.MemoryWrite{
			Scope: core.MemoryScopeTask, Subject: "task failed",
			Text: shortMemoryText("failed: "+reason, 4000), Provenance: provenance,
		})
	}
}

func (o *owner) addMemoryDelivery(request memoryDelivery) {
	if request.agent == "" || request.content == "" {
		return
	}
	if o.pendingMemory == nil {
		o.pendingMemory = map[core.AgentID][]memoryDelivery{}
	}
	name, ok := o.workspace.agentName(request.agent)
	if ok && o.sessions[name] != nil && o.sessions[name].State() == runtime.StateIdle {
		o.dispatchMemoryDelivery(request)
		return
	}
	o.pendingMemory[request.agent] = append(o.pendingMemory[request.agent], request)
}

func (o *owner) dispatchPendingMemory(agent core.AgentID) {
	if len(o.pendingMemory[agent]) == 0 {
		return
	}
	name, ok := o.workspace.agentName(agent)
	if !ok || o.sessions[name] == nil || o.sessions[name].State() != runtime.StateIdle {
		return
	}
	requests := o.pendingMemory[agent]
	delete(o.pendingMemory, agent)
	for _, request := range requests {
		o.dispatchMemoryDelivery(request)
	}
}

func (o *owner) dispatchMemoryDelivery(request memoryDelivery) {
	if o.router == nil {
		return
	}
	command := brokerCommand{apply: func(router *comms.Router) error {
		return router.SystemMessage(o.ctx, request.agent, request.thread, request.task, request.content)
	}}
	command.onFailure = func(current *owner, _ error) {
		if current.pendingMemory == nil {
			current.pendingMemory = map[core.AgentID][]memoryDelivery{}
		}
		current.pendingMemory[request.agent] = append(current.pendingMemory[request.agent], request)
	}
	o.enqueueBroker(command)
}

func memoryDeliveryContent(result core.MemoryRecallResult) string {
	if strings.TrimSpace(result.Context) == "" {
		return "Ghost memory recall returned no matching records."
	}
	return result.Context
}

func memoryRecallScopes(values []string, global bool) []core.MemoryScope {
	if len(values) == 0 {
		return memoryScopes(global)
	}
	scopes := make([]core.MemoryScope, 0, len(values))
	for _, value := range values {
		scopes = append(scopes, core.MemoryScope(value))
	}
	return scopes
}

func (c *Controller) RecallFor(agent, query string) (core.MemoryRecallResult, error) {
	if strings.TrimSpace(query) == "" {
		return core.MemoryRecallResult{}, errors.New("memory query is required")
	}
	var result core.MemoryRecallResult
	var done <-chan error
	callErr := c.call(func(o *owner) {
		if o.stopping || o.memory == nil || o.workspace == nil {
			return
		}
		agentID, ok := o.workspace.agentID(agent)
		if !ok {
			done = closedBrokerResult(errors.New("unknown memory requester"))
			return
		}
		taskID := o.taskForAgent(agentID)
		parents := memoryTaskParents(o.workspace, taskID)
		scopes := memoryScopes(o.config.Memory.ShareGlobal)
		done = o.enqueueBroker(brokerCommand{apply: func(_ *comms.Router) error {
			result = memoryContext(o.memory, o.ctx, agentID, taskID, parents, query, "operator_recall", scopes)
			return nil
		}})
	})
	if callErr != nil {
		return core.MemoryRecallResult{}, callErr
	}
	if done != nil {
		if err := <-done; err != nil {
			return core.MemoryRecallResult{}, err
		}
	}
	return result, nil
}

// RememberFromHuman queues one operator-authored memory record.
func (c *Controller) RememberFromHuman(agent string, scope core.MemoryScope, claim core.ClaimKind, text string, evidence, counterevidence []string) (bool, error) {
	var queued bool
	var done <-chan error
	callErr := c.call(func(o *owner) {
		if o.stopping {
			done = closedBrokerResult(ErrClosed)
			return
		}
		if o.memory == nil || o.workspace == nil {
			return
		}
		agentID, ok := o.workspace.agentID(agent)
		if !ok {
			done = closedBrokerResult(errors.New("unknown memory agent"))
			return
		}
		taskID := o.taskForAgent(agentID)
		workspaceID := o.workspace.workspaceID()
		done = o.enqueueBroker(brokerCommand{apply: func(_ *comms.Router) error {
			provenance := core.MemoryProvenance{
				Agent: agentID, Task: taskID, Workspace: string(workspaceID), ClaimKind: claim,
				Evidence: append([]string(nil), evidence...), Counterevidence: append([]string(nil), counterevidence...),
			}
			queued = o.enqueueMemoryWrite(core.MemoryWrite{Scope: scope, Text: shortMemoryText(text, 4000), Provenance: provenance})
			return nil
		}})
	})
	if callErr != nil {
		return false, callErr
	}
	if done != nil {
		if err := <-done; err != nil {
			return queued, err
		}
	}
	return queued, nil
}
