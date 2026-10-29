package run

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/runtime"
)

type EventFilter struct {
	Agent    string
	Category core.Category
	Type     string
	Search   string
}

type WorkspaceSnapshot struct {
	ID            core.WorkspaceID
	Agents        []core.Agent
	Tasks         []core.Task
	Messages      []core.Message
	Events        []core.Event
	Memory        MemoryHistory
	MemoryByTask  map[core.TaskID]MemoryHistory
	MemoryByAgent map[core.AgentID]MemoryHistory
}

type workspaceProjection struct {
	mu             sync.RWMutex
	id             core.WorkspaceID
	agents         map[core.AgentID]core.Agent
	agentOrder     []core.AgentID
	agentNames     map[string]core.AgentID
	tasks          map[core.TaskID]core.Task
	ownerChangedAt map[core.TaskID]time.Time
	taskOrder      []core.TaskID
	messages       map[core.MessageID]core.Message
	messageOrder   []core.MessageID
	eventRecords   []core.Event
	memory         MemoryHistory
	memoryByTask   map[core.TaskID]MemoryHistory
	memoryByAgent  map[core.AgentID]MemoryHistory
	sessionIDs     map[string]core.SessionID
}

func newWorkspaceProjection(id core.WorkspaceID, configured config.AgentSet, sourceOrder []string) (*workspaceProjection, error) {
	return newWorkspaceProjectionWithIDs(id, configured, sourceOrder, nil, nil)
}

func newWorkspaceProjectionWithIDs(id core.WorkspaceID, configured config.AgentSet, sourceOrder []string, ids *core.IDGenerator, identities map[string]core.AgentID) (*workspaceProjection, error) {
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("workspace ID: %w", err)
	}
	p := &workspaceProjection{
		id: id, agents: map[core.AgentID]core.Agent{}, agentNames: map[string]core.AgentID{},
		tasks: map[core.TaskID]core.Task{}, messages: map[core.MessageID]core.Message{},
		ownerChangedAt: map[core.TaskID]time.Time{},
		sessionIDs:     map[string]core.SessionID{},
	}
	names := config.OrderedAgentNames(configured, sourceOrder)
	for _, name := range names {
		backend := configured[name]
		agentID := identities[name]
		if agentID == "" {
			var rawID string
			var err error
			if ids == nil {
				rawID, err = core.NewID("agent")
			} else {
				rawID, err = ids.New("agent")
			}
			if err != nil {
				return nil, fmt.Errorf("make agent ID for %q: %w", name, err)
			}
			agentID = core.AgentID(rawID)
		}
		if err := agentID.Validate(); err != nil {
			return nil, fmt.Errorf("stored agent ID for %q: %w", name, err)
		}
		agent := core.Agent{
			ID: agentID, DisplayName: name,
			Runtime: core.RuntimeKind(backend.Runtime), Provider: core.ProviderKind(backend.Runtime),
			Availability: core.AvailabilityOffline, Activity: "offline",
			PermissionPolicy: core.PermissionPolicy{Mode: core.PermissionModeInherit},
		}
		if err := agent.Validate(); err != nil {
			return nil, fmt.Errorf("configured agent %q: %w", name, err)
		}
		p.agents[agent.ID] = agent
		p.agentOrder = append(p.agentOrder, agent.ID)
		p.agentNames[name] = agent.ID
	}
	return p, nil
}

func (p *workspaceProjection) workspaceID() core.WorkspaceID { return p.id }

func (p *workspaceProjection) agentID(name string) (core.AgentID, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if id, ok := p.agentNames[name]; ok {
		return id, true
	}
	id := core.AgentID(name)
	_, ok := p.agents[id]
	return id, ok
}

func (p *workspaceProjection) agentName(id core.AgentID) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for name, candidate := range p.agentNames {
		if candidate == id {
			return name, true
		}
	}
	return "", false
}

func (p *workspaceProjection) sessionID(providerID string) core.SessionID {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sessionIDs[providerID]
}

func (p *workspaceProjection) updateAgentSession(name string, session runtime.Session, taskID core.TaskID) (core.SessionID, error) {
	if session == nil {
		return "", fmt.Errorf("agent session is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	agentID, ok := p.agentNames[name]
	if !ok {
		return "", fmt.Errorf("unknown agent %q", name)
	}
	agent := p.agents[agentID]
	providerID := session.ID()
	sessionID := p.sessionIDs[providerID]
	if sessionID == "" {
		rawID, err := core.NewID("session")
		if err != nil {
			return "", fmt.Errorf("make session ID for %q: %w", name, err)
		}
		sessionID = core.SessionID(rawID)
		p.sessionIDs[providerID] = sessionID
	}
	ref := session.Metadata().ThreadID
	if ref == "" {
		ref = providerID
	}
	agent.SessionBinding = &core.SessionBinding{SessionID: sessionID, ProviderSessionRef: ref}
	state := session.State()
	switch state {
	case runtime.StateIdle, runtime.StateStarting:
		agent.Availability, agent.Activity = core.AvailabilityAvailable, "available"
	case runtime.StateRunning:
		agent.Availability, agent.Activity = core.AvailabilityBusy, "running"
	case runtime.StateInterrupting:
		agent.Availability, agent.Activity = core.AvailabilityBusy, "interrupting"
	case runtime.StateFailed:
		agent.Availability, agent.Activity = core.AvailabilityError, "failed"
	default:
		agent.Availability, agent.Activity = core.AvailabilityOffline, "offline"
	}
	if taskID != "" {
		agent.ActiveTask = uniqueTaskIDs(agent.ActiveTask, taskID)
	}
	p.agents[agentID] = agent
	return sessionID, nil
}

func (p *workspaceProjection) setAgentState(name string, availability core.Availability, activity string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.agentNames[name]; ok {
		agent := p.agents[id]
		agent.Availability, agent.Activity = availability, activity
		p.agents[id] = agent
	}
}

func (p *workspaceProjection) addTask(task core.Task) error {
	if err := task.Validate(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.tasks[task.ID]; exists {
		return fmt.Errorf("task %q already exists", task.ID)
	}
	if _, ok := p.agents[task.Owner]; !ok {
		return fmt.Errorf("unknown task owner %q", task.Owner)
	}
	p.tasks[task.ID] = cloneTask(task)
	p.taskOrder = append(p.taskOrder, task.ID)
	p.assignTaskLocked(task.ID, task.Owner)
	for _, id := range task.Participants {
		p.assignAgentTaskLocked(id, task.ID)
	}
	return nil
}

func (p *workspaceProjection) transitionTask(id core.TaskID, to core.TaskStatus, at time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	task, ok := p.tasks[id]
	if !ok {
		return fmt.Errorf("unknown task %q", id)
	}
	if err := task.Transition(to, at); err != nil {
		return err
	}
	p.tasks[id] = cloneTask(task)
	for _, agentID := range append([]core.AgentID{task.Owner}, task.Participants...) {
		agent := p.agents[agentID]
		if to == core.TaskRunning {
			agent.ActiveTask = uniqueTaskIDs(agent.ActiveTask, id)
		} else if to.IsTerminal() {
			agent.ActiveTask = removeTaskID(agent.ActiveTask, id)
		}
		p.agents[agentID] = agent
	}
	return nil
}

func (p *workspaceProjection) assignTask(id core.TaskID, name string) error {
	return p.assignTaskAt(id, name, timeNow().UTC())
}

func (p *workspaceProjection) assignTaskAt(id core.TaskID, name string, at time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	task, ok := p.tasks[id]
	if !ok {
		return fmt.Errorf("unknown task %q", id)
	}
	agentID, ok := p.agentNames[name]
	if !ok {
		return fmt.Errorf("unknown agent %q", name)
	}
	if task.Owner != agentID {
		task.Owner = agentID
		task.Participants = uniqueAgentIDs(task.Participants, agentID)
		if at.IsZero() {
			at = timeNow().UTC()
		}
		if at.After(task.UpdatedAt) {
			task.UpdatedAt = at.UTC()
		}
		p.ownerChangedAt[id] = at.UTC()
		p.tasks[id] = cloneTask(task)
	}
	p.assignAgentTaskLocked(agentID, id)
	if task.Status == core.TaskRunning {
		agent := p.agents[agentID]
		agent.ActiveTask = uniqueTaskIDs(agent.ActiveTask, id)
		p.agents[agentID] = agent
	}
	return nil
}

func (p *workspaceProjection) assignTaskLocked(id core.TaskID, agentID core.AgentID) {
	p.assignAgentTaskLocked(agentID, id)
}

func (p *workspaceProjection) assignAgentTaskLocked(agentID core.AgentID, taskID core.TaskID) {
	agent, ok := p.agents[agentID]
	if !ok {
		return
	}
	agent.AssignedTasks = uniqueTaskIDs(agent.AssignedTasks, taskID)
	p.agents[agentID] = agent
}

func (p *workspaceProjection) addMessage(message core.Message) error {
	if err := message.Validate(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.messages[message.ID]; exists {
		return fmt.Errorf("message %q already exists", message.ID)
	}
	p.messages[message.ID] = cloneMessage(message)
	p.messageOrder = append(p.messageOrder, message.ID)
	return nil
}

func (p *workspaceProjection) updateMessageDelivery(id core.MessageID, status core.DeliveryStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	message, ok := p.messages[id]
	if ok {
		message.Delivery = status
		p.messages[id] = message
	}
}

// replaceBrokerRecords copies router-owned tasks and messages into the read
// projection. Pending controller owner changes survive until the router event
// snapshot catches up.
func (p *workspaceProjection) replaceBrokerRecords(tasks []core.Task, messages []core.Message, events []core.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := p.tasks
	routerOwnerChangedAt := latestOwnerChangeTimes(events)
	p.tasks = make(map[core.TaskID]core.Task, len(tasks))
	p.taskOrder = p.taskOrder[:0]
	p.messages = make(map[core.MessageID]core.Message, len(messages))
	p.messageOrder = p.messageOrder[:0]
	for id, agent := range p.agents {
		agent.AssignedTasks = nil
		agent.ActiveTask = nil
		p.agents[id] = agent
	}
	for _, task := range tasks {
		if task.Validate() != nil {
			continue
		}
		if _, ok := p.agents[task.Owner]; !ok {
			continue
		}
		previousTask := previous[task.ID]
		task = keepNewerTaskStatus(task, previousTask, routerOwnerChangedAt[task.ID], p.ownerChangedAt[task.ID])
		if ownerChangedAt := p.ownerChangedAt[task.ID]; !ownerChangedAt.IsZero() &&
			!routerOwnerChangedAt[task.ID].Before(ownerChangedAt) && task.Owner == previousTask.Owner {
			delete(p.ownerChangedAt, task.ID)
		}
		p.tasks[task.ID] = cloneTask(task)
		p.taskOrder = append(p.taskOrder, task.ID)
		p.assignAgentTaskLocked(task.Owner, task.ID)
		for _, participant := range task.Participants {
			p.assignAgentTaskLocked(participant, task.ID)
		}
		if task.Status == core.TaskRunning {
			for _, id := range append([]core.AgentID{task.Owner}, task.Participants...) {
				if agent, ok := p.agents[id]; ok {
					agent.ActiveTask = uniqueTaskIDs(agent.ActiveTask, task.ID)
					p.agents[id] = agent
				}
			}
		}
	}
	for _, message := range messages {
		if message.Validate() != nil {
			continue
		}
		p.messages[message.ID] = cloneMessage(message)
		p.messageOrder = append(p.messageOrder, message.ID)
	}
}

// keepNewerTaskStatus stops a stale router snapshot moving a task backwards.
// The controller projects its own active-task transitions immediately and
// queues the same transition to the router. A snapshot taken before that queued
// command runs still shows the older status. Router transitions only move
// forward, so a lower-ranked router status is stale and the projected status,
// timing and failure reason are kept until the router catches up.
func keepNewerTaskStatus(router, projected core.Task, routerOwnerChangedAt, projectedOwnerChangedAt time.Time) core.Task {
	if projected.ID != router.ID {
		return router
	}
	merged := router
	if taskStatusRank(projected.Status) > taskStatusRank(router.Status) {
		merged.Status = projected.Status
		merged.StartedAt = projected.StartedAt
		merged.FinishedAt = projected.FinishedAt
		merged.FailureReason = projected.FailureReason
		if projected.UpdatedAt.After(merged.UpdatedAt) {
			merged.UpdatedAt = projected.UpdatedAt
		}
	}
	if projectedOwnerChangedAt.After(routerOwnerChangedAt) {
		merged.Owner = projected.Owner
		for _, participant := range projected.Participants {
			merged.Participants = uniqueAgentIDs(merged.Participants, participant)
		}
		merged.Participants = uniqueAgentIDs(merged.Participants, projected.Owner)
		if projected.UpdatedAt.After(merged.UpdatedAt) {
			merged.UpdatedAt = projected.UpdatedAt
		}
	}
	if merged.Validate() != nil {
		return router
	}
	return merged
}

func latestOwnerChangeTimes(events []core.Event) map[core.TaskID]time.Time {
	latest := make(map[core.TaskID]time.Time)
	for _, item := range events {
		if item.Type == "task.owner_changed" && item.TaskID != "" && item.Time.After(latest[item.TaskID]) {
			latest[item.TaskID] = item.Time
		}
	}
	return latest
}

func taskStatusRank(status core.TaskStatus) int {
	switch {
	case status == core.TaskQueued:
		return 0
	case status.IsTerminal():
		return 2
	default:
		return 1
	}
}

func (p *workspaceProjection) addEvent(event core.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	event.Payload = append(event.Payload[:0:0], event.Payload...)
	p.eventRecords = append(p.eventRecords, event)
	p.projectMemoryEventLocked(event)
	return nil
}

func (p *workspaceProjection) messagesForAgent(name string) []core.Message {
	p.mu.RLock()
	defer p.mu.RUnlock()
	agentID, ok := p.resolveAgentLocked(name)
	if !ok {
		return nil
	}
	out := make([]core.Message, 0)
	for _, id := range p.messageOrder {
		message := p.messages[id]
		if message.Sender == agentID || containsAgent(message.Recipients, agentID) || message.Type == core.MessageBroadcast {
			out = append(out, cloneMessage(message))
		}
	}
	return out
}

func (p *workspaceProjection) allMessages() []core.Message {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]core.Message, 0, len(p.messageOrder))
	for _, id := range p.messageOrder {
		out = append(out, cloneMessage(p.messages[id]))
	}
	return out
}

func (p *workspaceProjection) tasksForAgent(name string) []core.Task {
	p.mu.RLock()
	defer p.mu.RUnlock()
	agentID, ok := p.resolveAgentLocked(name)
	if !ok {
		return nil
	}
	out := make([]core.Task, 0)
	for _, id := range p.taskOrder {
		task := p.tasks[id]
		if task.Owner == agentID || containsAgent(task.Participants, agentID) {
			out = append(out, cloneTask(task))
		}
	}
	return out
}

func (p *workspaceProjection) events(filter EventFilter) []core.Event {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var agentID core.AgentID
	if filter.Agent != "" {
		var ok bool
		agentID, ok = p.resolveAgentLocked(filter.Agent)
		if !ok {
			return nil
		}
	}
	search := strings.ToLower(filter.Search)
	out := make([]core.Event, 0)
	for _, item := range p.eventRecords {
		if filter.Category != "" && item.Category != filter.Category {
			continue
		}
		if filter.Type != "" && item.Type != filter.Type {
			continue
		}
		if agentID != "" && item.AgentID != agentID && !(item.Actor.Kind == core.ActorAgent && item.Actor.ID == string(agentID)) {
			continue
		}
		if search != "" {
			haystack := strings.ToLower(item.Type + " " + item.Source + " " + item.Actor.ID + " " + string(item.Payload))
			if !strings.Contains(haystack, search) {
				continue
			}
		}
		out = append(out, cloneCoreEvent(item))
	}
	return out
}

func (p *workspaceProjection) snapshot() WorkspaceSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := WorkspaceSnapshot{
		ID: p.id, Memory: cloneMemoryHistory(p.memory),
		MemoryByTask:  make(map[core.TaskID]MemoryHistory, len(p.memoryByTask)),
		MemoryByAgent: make(map[core.AgentID]MemoryHistory, len(p.memoryByAgent)),
	}
	for _, id := range p.agentOrder {
		out.Agents = append(out.Agents, cloneAgent(p.agents[id]))
	}
	for _, id := range p.taskOrder {
		out.Tasks = append(out.Tasks, cloneTask(p.tasks[id]))
	}
	for _, id := range p.messageOrder {
		out.Messages = append(out.Messages, cloneMessage(p.messages[id]))
	}
	for _, item := range p.eventRecords {
		out.Events = append(out.Events, cloneCoreEvent(item))
	}
	for id, history := range p.memoryByTask {
		out.MemoryByTask[id] = cloneMemoryHistory(history)
	}
	for id, history := range p.memoryByAgent {
		out.MemoryByAgent[id] = cloneMemoryHistory(history)
	}
	return out
}

func (p *workspaceProjection) resolveRecipients(explicit []string, text string) ([]core.AgentID, bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]core.AgentID, 0, len(explicit))
	broadcast := false
	add := func(name string) error {
		name = strings.TrimSpace(name)
		if strings.EqualFold(name, "all") || name == string(core.BroadcastWildcard) {
			broadcast = true
			return nil
		}
		id, ok := p.resolveAgentLocked(name)
		if !ok {
			return fmt.Errorf("unknown agent %q", name)
		}
		ids = uniqueAgentIDs(ids, id)
		return nil
	}
	for _, name := range explicit {
		if err := add(name); err != nil {
			return nil, false, err
		}
	}
	for _, token := range strings.Fields(text) {
		token = strings.Trim(token, ".,:;!?()[]{}<>\"'")
		if !strings.HasPrefix(token, "@") {
			continue
		}
		name := strings.TrimPrefix(token, "@")
		if name == "" {
			continue
		}
		if err := add(name); err != nil {
			return nil, false, err
		}
	}
	if broadcast {
		return []core.AgentID{core.BroadcastWildcard}, true, nil
	}
	return ids, false, nil
}

func (p *workspaceProjection) resolveAgentLocked(name string) (core.AgentID, bool) {
	if id, ok := p.agentNames[name]; ok {
		return id, true
	}
	id := core.AgentID(name)
	_, ok := p.agents[id]
	return id, ok
}

func uniqueTaskIDs(items []core.TaskID, id core.TaskID) []core.TaskID {
	for _, item := range items {
		if item == id {
			return append([]core.TaskID(nil), items...)
		}
	}
	return append(append([]core.TaskID(nil), items...), id)
}

func removeTaskID(items []core.TaskID, id core.TaskID) []core.TaskID {
	out := make([]core.TaskID, 0, len(items))
	for _, item := range items {
		if item != id {
			out = append(out, item)
		}
	}
	return out
}

func uniqueAgentIDs(items []core.AgentID, id core.AgentID) []core.AgentID {
	for _, item := range items {
		if item == id {
			return append([]core.AgentID(nil), items...)
		}
	}
	return append(append([]core.AgentID(nil), items...), id)
}

func containsAgent(items []core.AgentID, id core.AgentID) bool {
	for _, item := range items {
		if item == id {
			return true
		}
	}
	return false
}

func cloneAgent(agent core.Agent) core.Agent {
	agent.Capabilities = append([]core.Capability(nil), agent.Capabilities...)
	agent.AssignedTasks = append([]core.TaskID(nil), agent.AssignedTasks...)
	agent.ActiveTask = append([]core.TaskID(nil), agent.ActiveTask...)
	if agent.SessionBinding != nil {
		binding := *agent.SessionBinding
		agent.SessionBinding = &binding
	}
	agent.PermissionPolicy.AllowTools = append([]string(nil), agent.PermissionPolicy.AllowTools...)
	agent.PermissionPolicy.DenyTools = append([]string(nil), agent.PermissionPolicy.DenyTools...)
	return agent
}

func cloneTask(task core.Task) core.Task {
	task.Participants = append([]core.AgentID(nil), task.Participants...)
	task.DependsOn = append([]core.TaskID(nil), task.DependsOn...)
	task.EventRefs = append([]core.EventID(nil), task.EventRefs...)
	task.ContextRefs = append([]string(nil), task.ContextRefs...)
	if task.Parent != nil {
		parent := *task.Parent
		task.Parent = &parent
	}
	if task.PermissionPolicy != nil {
		policy := *task.PermissionPolicy
		policy.AllowTools = append([]string(nil), policy.AllowTools...)
		policy.DenyTools = append([]string(nil), policy.DenyTools...)
		task.PermissionPolicy = &policy
	}
	if task.StartedAt != nil {
		started := *task.StartedAt
		task.StartedAt = &started
	}
	if task.FinishedAt != nil {
		finished := *task.FinishedAt
		task.FinishedAt = &finished
	}
	if task.Limits.Budget.Deadline != nil {
		deadline := *task.Limits.Budget.Deadline
		task.Limits.Budget.Deadline = &deadline
	}
	return task
}

func cloneMessage(message core.Message) core.Message {
	message.Recipients = append([]core.AgentID(nil), message.Recipients...)
	if message.RecipientDelivery != nil {
		states := make(map[core.AgentID]core.DeliveryStatus, len(message.RecipientDelivery))
		for recipient, state := range message.RecipientDelivery {
			states[recipient] = state
		}
		message.RecipientDelivery = states
	}
	if message.DeliveryReason != nil {
		reasons := make(map[core.AgentID]string, len(message.DeliveryReason))
		for recipient, reason := range message.DeliveryReason {
			reasons[recipient] = reason
		}
		message.DeliveryReason = reasons
	}
	if message.TaskID != nil {
		taskID := *message.TaskID
		message.TaskID = &taskID
	}
	if message.ReplyTo != nil {
		replyTo := *message.ReplyTo
		message.ReplyTo = &replyTo
	}
	return message
}

func cloneCoreEvent(event core.Event) core.Event {
	event.Payload = append([]byte(nil), event.Payload...)
	return event
}
