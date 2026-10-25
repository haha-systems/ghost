package run

import (
	"fmt"
	"sort"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
)

func (o *owner) eventContext() eventContext {
	if o.workspace == nil {
		return eventContext{}
	}
	ctx := eventContext{Workspace: o.workspace.workspaceID(), IDs: o.ids, ResourceAgents: map[string]string{}}
	ctx.AgentIDs = make(map[string]core.AgentID)
	ctx.SessionIDs = make(map[string]core.SessionID)
	o.workspace.mu.RLock()
	for name, id := range o.workspace.agentNames {
		ctx.AgentIDs[name] = id
	}
	for providerID, id := range o.workspace.sessionIDs {
		ctx.SessionIDs[providerID] = id
	}
	o.workspace.mu.RUnlock()
	if o.config.QAC.Resources != nil {
		for resourceID, resource := range o.config.QAC.Resources {
			ctx.ResourceAgents[resourceID] = resource.Agent
		}
	}
	ctx.TaskID = o.activeTaskID
	if task, ok := o.workspace.task(ctx.TaskID); ok {
		ctx.ThreadID = task.ThreadID
		ctx.CorrelationID = o.taskCorrelation[ctx.TaskID]
	}
	return ctx
}

func (o *owner) storeCoreEvent(item core.Event) (core.Event, error) {
	if o.workspace == nil || o.coreSequencer == nil {
		return core.Event{}, fmt.Errorf("workspace projection is not initialized")
	}
	var sequenced core.Event
	var err error
	if o.eventLog != nil {
		sequenced, err = o.eventLog.Append(item)
		if err == nil {
			_ = o.coreSequencer.Restore(sequenced.StreamKey(), sequenced.Seq)
		} else {
			o.failEventLog(err)
			sequenced, err = o.coreSequencer.Assign(item)
		}
	} else {
		sequenced, err = o.coreSequencer.Assign(item)
	}
	if err != nil {
		return core.Event{}, err
	}
	if err := o.workspace.addEvent(sequenced); err != nil {
		return core.Event{}, err
	}
	return sequenced, nil
}

func (o *owner) failEventLog(err error) {
	if err == nil || o.eventLogError != nil {
		return
	}
	o.eventLogError = err
	o.emit(Event{Entry: event.Event{Time: timeNow(), Source: "CORE", Kind: event.KindError, Message: "core event log write failed: " + err.Error()}})
}

func (o *owner) publishCoreEvent(item core.Event) {
	stored, err := o.storeCoreEvent(item)
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.emit(Event{CoreEvent: &stored})
}

func (o *owner) coreProjectionError(err error) {
	if err == nil {
		return
	}
	o.projectionError = err
	o.emit(Event{Entry: event.Event{Time: timeNow(), Source: "CORE", Kind: event.KindError, Message: "project core event: " + err.Error()}})
}

func (o *owner) projectLegacy(agent string, item event.Event) *core.Event {
	if o.workspace == nil {
		return nil
	}
	if item.Kind == event.KindQAC {
		return nil // The matching structured cognition.Plan is recorded separately.
	}
	if item.SessionID != "" {
		if _, err := o.workspace.ensureSessionID(item.SessionID); err != nil {
			o.coreProjectionError(err)
		}
	}
	converted, err := adaptLegacyEvent(o.eventContext(), agent, item)
	if err != nil {
		o.coreProjectionError(err)
		return nil
	}
	stored, err := o.storeCoreEvent(converted)
	if err != nil {
		o.coreProjectionError(err)
		return nil
	}
	return &stored
}

func (o *owner) projectRuntime(item runtime.Event) *core.Event {
	if o.workspace == nil {
		return nil
	}
	if item.SessionID != "" {
		if _, err := o.workspace.ensureSessionID(item.SessionID); err != nil {
			o.coreProjectionError(err)
		}
	}
	converted, err := adaptRuntimeEvent(o.eventContext(), item)
	if err != nil {
		o.coreProjectionError(err)
		return nil
	}
	stored, err := o.storeCoreEvent(converted)
	if err != nil {
		o.coreProjectionError(err)
		return nil
	}
	return &stored
}

func (o *owner) projectPhase(item cognition.PhaseEvent) *core.Event {
	if o.workspace == nil {
		return nil
	}
	if item.SessionID != "" {
		if _, err := o.workspace.ensureSessionID(item.SessionID); err != nil {
			o.coreProjectionError(err)
		}
	}
	converted, err := adaptPhaseEvent(o.eventContext(), item)
	if err != nil {
		o.coreProjectionError(err)
		return nil
	}
	stored, err := o.storeCoreEvent(converted)
	if err != nil {
		o.coreProjectionError(err)
		return nil
	}
	return &stored
}

func (o *owner) projectQACDecision(plan cognition.Plan) {
	converted, err := adaptQACDecision(o.eventContext(), plan)
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.publishCoreEvent(converted)
}

func (o *owner) assignProjectedTask(agentName string) {
	before, ok := o.workspace.task(o.activeTaskID)
	if !ok {
		return
	}
	if err := o.workspace.assignTask(before.ID, agentName); err != nil {
		o.coreProjectionError(err)
		return
	}
	after, _ := o.workspace.task(before.ID)
	if before.Owner == after.Owner {
		return
	}
	payload := map[string]any{"task_id": before.ID, "from": before.Owner, "to": after.Owner}
	item, err := newCoreEvent(o.eventContext(), timeNow(), core.Actor{Kind: core.ActorSystem, ID: "controller"}, "controller", "task.owner_changed", after.Owner, "", payload)
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.publishCoreEvent(item)
}

func (o *owner) startStandardWork(goal string) (<-chan error, error) {
	if o.workspace == nil {
		return nil, fmt.Errorf("workspace projection is not initialized")
	}
	names := make([]string, 0)
	if o.startBroadcast {
		names = o.workspace.agentNamesSnapshot()
	} else if len(o.startRecipients) > 0 {
		for _, id := range o.startRecipients {
			if name, ok := o.workspace.agentName(id); ok {
				names = append(names, name)
			}
		}
	} else {
		for _, name := range o.workspace.agentNamesSnapshot() {
			if session := o.sessions[name]; session != nil && session.State() == runtime.StateIdle {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no available agent sessions")
	}
	participants := make([]core.AgentID, 0, len(names))
	for _, name := range names {
		id, ok := o.workspace.agentID(name)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q", name)
		}
		participants = uniqueAgentIDs(participants, id)
	}
	task, err := o.createProjectedTask(goal, names[0], participants, core.CESOff)
	if err != nil {
		return nil, err
	}
	pending := make(map[string]bool, len(names))
	for _, name := range names {
		pending[name] = true
	}
	o.taskPendingAgents[task.ID] = pending
	message, err := o.humanMessage(goal, func() []core.AgentID {
		if o.startBroadcast {
			return []core.AgentID{core.BroadcastWildcard}
		}
		return participants
	}(), o.startBroadcast, task.ID)
	if err != nil {
		return nil, err
	}
	return o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
		_, err := router.Send(o.ctx, message)
		return err
	}}), nil
}

func (o *owner) createProjectedTask(goal string, ownerName string, participants []core.AgentID, policy core.CESPolicy) (core.Task, error) {
	agentID, ok := o.workspace.agentID(ownerName)
	if !ok {
		return core.Task{}, fmt.Errorf("unknown task owner %q", ownerName)
	}
	taskID, err := o.ids.New("task")
	if err != nil {
		return core.Task{}, err
	}
	threadID, err := o.ids.New("thread")
	if err != nil {
		return core.Task{}, err
	}
	now := timeNow().UTC()
	if len(participants) == 0 {
		participants = []core.AgentID{agentID}
	} else {
		participants = uniqueAgentIDs(participants, agentID)
	}
	task := core.Task{
		ID: core.TaskID(taskID), Requester: core.HumanAgentID, Owner: agentID,
		Participants: participants, Status: core.TaskQueued,
		CreatedAt: now, UpdatedAt: now, CESPolicy: policy, ThreadID: core.ThreadID(threadID),
	}
	if err := o.workspace.addTask(task); err != nil {
		return core.Task{}, err
	}
	o.activeTaskID = task.ID
	if o.taskCorrelation == nil {
		o.taskCorrelation = map[core.TaskID]core.EventID{}
	}
	payload := map[string]any{"task_id": task.ID, "from": "", "to": core.TaskQueued, "goal": goal, "owner": agentID}
	eventItem, err := newCoreEvent(o.eventContext(), now, core.Actor{Kind: core.ActorOperator, ID: string(core.HumanAgentID)}, "controller", "task.status_changed", "", "", payload)
	if err != nil {
		return core.Task{}, err
	}
	// The first task event is the stable correlation root for the task.
	eventItem.CorrelationID = eventItem.ID
	eventItem, err = o.storeCoreEvent(eventItem)
	if err != nil {
		return core.Task{}, err
	}
	o.taskCorrelation[task.ID] = eventItem.ID
	o.emit(Event{CoreEvent: &eventItem})
	o.queueBroker(brokerCommand{apply: func(router *comms.Router) error { return router.RegisterTask(task) }})
	return task, nil
}

func (o *owner) humanMessage(text string, recipients []core.AgentID, broadcast bool, taskID core.TaskID) (core.Message, error) {
	task, ok := o.workspace.task(taskID)
	if !ok {
		return core.Message{}, fmt.Errorf("no task is active for the human message")
	}
	kind := core.MessageRequest
	if broadcast {
		kind = core.MessageBroadcast
		recipients = []core.AgentID{core.BroadcastWildcard}
	}
	taskCopy := task.ID
	return core.Message{Sender: core.HumanAgentID, Recipients: recipients, ThreadID: task.ThreadID, TaskID: &taskCopy, Type: kind, Content: text}, nil
}

func (o *owner) transitionProjectedTask(to core.TaskStatus, reason string) {
	task, ok := o.workspace.task(o.activeTaskID)
	if !ok || task.Status == to || task.Status.IsTerminal() {
		return
	}
	from := task.Status
	if err := o.workspace.transitionTask(task.ID, to, timeNow().UTC()); err != nil {
		o.coreProjectionError(err)
		return
	}
	payload := map[string]any{"task_id": task.ID, "from": from, "to": to}
	if reason != "" {
		payload["reason"] = reason
	}
	ctx := o.eventContext()
	agentID := task.Owner
	item, err := newCoreEvent(ctx, timeNow(), core.Actor{Kind: core.ActorSystem, ID: "controller"}, "controller", "task.status_changed", agentID, "", payload)
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.publishCoreEvent(item)
	if o.router != nil {
		taskID := task.ID
		o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
			return router.TransitionTask(taskID, to, reason)
		}})
	}
}

func (o *owner) updateWorkspaceSessions(sessions map[string]runtime.Session) {
	for name := range o.workspace.agentNames {
		if session := sessions[name]; session != nil {
			if _, err := o.workspace.updateAgentSession(name, session, o.activeTaskID); err != nil {
				o.coreProjectionError(err)
			}
		} else {
			o.workspace.setAgentState(name, core.AvailabilityOffline, "offline")
		}
	}
}

func (o *owner) hasActiveRun() bool {
	if o.cesActive() {
		return true
	}
	if o.workspace != nil {
		if task, ok := o.workspace.task(o.activeTaskID); ok && !task.Status.IsTerminal() {
			return true
		}
	}
	return false
}

func (o *owner) providerTurnActive() bool {
	if o.cesActive() {
		return true
	}
	for _, session := range o.sessions {
		if session != nil && (session.State() == runtime.StateRunning || session.State() == runtime.StateInterrupting) {
			return true
		}
	}
	return false
}

func validCESPolicy(policy CESPolicy) bool {
	return policy == CESPolicyOn || policy == CESPolicyOff
}

func (o *owner) resolveCESPolicy(requested CESPolicy) (CESPolicy, string) {
	if requested == CESPolicyOn && o.cognition == nil {
		return CESPolicyOff, "unsupported: QAC not configured"
	}
	return requested, ""
}

func (o *owner) requestCESPolicy(policy CESPolicy) {
	o.cesRequested = policy
	effective, reason := o.resolveCESPolicy(policy)
	if o.providerTurnActive() && effective != o.cesEffective {
		o.cesPending = true
		o.cesPolicyReason = "pending until next Start"
	} else {
		o.cesEffective = effective
		o.cesPending = false
		o.cesPolicyReason = reason
	}
	o.emitCESPolicyChanged()
}

func (o *owner) applyRequestedCESPolicy() {
	effective, reason := o.resolveCESPolicy(o.cesRequested)
	o.cesEffective = effective
	o.cesPending = false
	o.cesPolicyReason = reason
	o.emitCESPolicyChanged()
}

func (o *owner) emitCESPolicyChanged() {
	ctx := o.eventContext()
	ctx.TaskID, ctx.ThreadID, ctx.CorrelationID = "", "", ""
	payload := map[string]any{
		"requested": o.cesRequested, "effective": o.cesEffective,
		"pending": o.cesPending, "reason": o.cesPolicyReason,
	}
	item, err := newCoreEvent(ctx, timeNow(), core.Actor{Kind: core.ActorOperator, ID: string(core.HumanAgentID)}, "controller", "ces.policy_changed", "", "", payload)
	if err != nil {
		o.coreProjectionError(err)
		return
	}
	o.publishCoreEvent(item)
}

func (o *owner) projectObservedRuntime(item runtime.Event) {
	if session := o.sessions[item.AgentID]; session != nil {
		if _, err := o.workspace.updateAgentSession(item.AgentID, session, o.activeTaskID); err != nil {
			o.coreProjectionError(err)
		}
	}
	coreItem := o.projectRuntime(item)
	if coreItem != nil {
		o.emit(Event{AgentID: item.AgentID, CoreEvent: coreItem})
	}
	method := item.Metadata["backend_method"]
	if !o.cesActive() && item.AgentID != "" {
		o.observeStandardTurn(item.AgentID, method, item.Summary)
	}
	if agentID, ok := o.workspace.agentID(item.AgentID); ok {
		if taskID := o.agentTaskIDs[agentID]; taskID != "" && taskID != o.activeTaskID {
			switch method {
			case "turn/started":
				o.queueBroker(brokerCommand{apply: func(router *comms.Router) error { return router.TransitionTask(taskID, core.TaskRunning, "") }})
			case "turn/failed":
				o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
					_, err := router.Fail(o.ctx, taskID, item.Summary)
					return err
				}})
			}
		}
		if session := o.sessions[item.AgentID]; session != nil && session.State() == runtime.StateIdle {
			o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
				router.AgentAvailable(agentID)
				return nil
			}})
		}
	}
	if item.Kind == runtime.KindMessage {
		if method == "item/agentMessage/delta" {
			key := item.SessionID + "/" + item.TurnID
			o.replyBuffers[key] += item.Summary
		} else if method == "item/agentMessage/completed" {
			key := item.SessionID + "/" + item.TurnID
			text := item.Summary
			if text == "" {
				text = o.replyBuffers[key]
			}
			delete(o.replyBuffers, key)
			o.handleAgentFinal(item, text, coreEventID(coreItem))
		} else {
			o.handleAgentFinal(item, item.Summary, coreEventID(coreItem))
		}
	}
}

func coreEventID(item *core.Event) core.EventID {
	if item == nil {
		return ""
	}
	return item.ID
}

func (o *owner) observeStandardTurn(agent, method, detail string) {
	task, ok := o.workspace.task(o.activeTaskID)
	if !ok || task.Status.IsTerminal() {
		return
	}
	pending := o.taskPendingAgents[task.ID]
	if !pending[agent] {
		return
	}
	switch method {
	case "turn/started":
		o.transitionProjectedTask(core.TaskRunning, "")
	case "turn/completed":
		if task.Status == core.TaskQueued {
			o.transitionProjectedTask(core.TaskRunning, "provider reported completion without a start event")
		}
		pending[agent] = false
		o.maybeCompleteParent(task.ID)
	case "turn/failed":
		pending[agent] = false
		o.transitionProjectedTask(core.TaskFailed, detail)
	}
}

func (c *Controller) WorkspaceSnapshot() WorkspaceSnapshot {
	var snapshot WorkspaceSnapshot
	if err := c.call(func(o *owner) { snapshot = o.workspace.snapshot() }); err != nil {
		return WorkspaceSnapshot{}
	}
	return snapshot
}

func (c *Controller) MessagesForAgent(id string) []core.Message {
	var messages []core.Message
	_ = c.call(func(o *owner) { messages = o.workspace.messagesForAgent(id) })
	return messages
}

func (c *Controller) AllMessages() []core.Message {
	var messages []core.Message
	_ = c.call(func(o *owner) { messages = o.workspace.allMessages() })
	return messages
}

func (c *Controller) TasksForAgent(id string) []core.Task {
	var tasks []core.Task
	_ = c.call(func(o *owner) { tasks = o.workspace.tasksForAgent(id) })
	return tasks
}

func (c *Controller) Events(filter EventFilter) []core.Event {
	var items []core.Event
	_ = c.call(func(o *owner) { items = o.workspace.events(filter) })
	return items
}

func (p *workspaceProjection) task(id core.TaskID) (core.Task, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	task, ok := p.tasks[id]
	return cloneTask(task), ok
}

func (p *workspaceProjection) ensureSessionID(providerID string) (core.SessionID, error) {
	if providerID == "" {
		return "", nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if id := p.sessionIDs[providerID]; id != "" {
		return id, nil
	}
	rawID, err := core.NewID("session")
	if err != nil {
		return "", fmt.Errorf("make provider session ID: %w", err)
	}
	id := core.SessionID(rawID)
	p.sessionIDs[providerID] = id
	return id, nil
}

func (p *workspaceProjection) agentNamesSnapshot() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.agentNames))
	for name := range p.agentNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
