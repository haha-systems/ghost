package run

import (
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/runtime"
)

func (o *owner) handleAgentFinal(item runtime.Event, text string, cause core.EventID) {
	if strings.TrimSpace(text) == "" || o.workspace == nil {
		return
	}
	agentID, ok := o.workspace.agentID(item.AgentID)
	if !ok {
		return
	}
	consultation, isConsultationReply := o.openConsultationFor(agentID)
	request, found, parseErr := cognition.ParseAgentChannel(text)
	lookup := o.channelTaskLookup(request)
	contextTask := o.taskForAgent(agentID)
	messageTask := channelMessageTask(request, contextTask)
	channelTask := pendingChannelTask(request, contextTask, lookup)
	if found || parseErr != nil {
		o.beginPendingChannel(channelTask)
	} else {
		o.beginPendingChannel(messageTask)
	}
	turnKey := item.SessionID + "/" + item.TurnID
	command := brokerCommand{notifyAgent: agentID, notifyTurn: turnKey, rejection: routingRequestFor(request, contextTask, o.workspace, lookup)}
	if found && parseErr != nil {
		command.rejection = routingRequestForParseError(parseErr, channelTask)
	}
	if found || parseErr != nil {
		command.pendingTaskID = channelTask
	} else {
		command.pendingTaskID = messageTask
	}
	if !found && parseErr == nil && isConsultationReply {
		command.pendingTaskID = consultation.ParentTaskID
		o.beginPendingChannel(command.pendingTaskID)
		ctx := o.ctx
		result := comms.ConsultationResult{Findings: text, Uncertainty: "not specified in the consultant's reply"}
		command.apply = func(router *comms.Router) error {
			_, err := router.ResolveConsultation(ctx, consultation.ID, agentID, result)
			return err
		}
		o.enqueueBroker(command)
		return
	}
	if request.Kind == cognition.AgentChannelResult {
		if parent, ok := lookup(channelTask); ok {
			if name, exists := o.workspace.agentName(parent.Owner); exists {
				pending := o.taskPendingAgents[channelTask]
				wasSet, hadEntry := pending[name]
				wasMap := pending != nil
				if pending == nil {
					pending = map[string]bool{}
					o.taskPendingAgents[channelTask] = pending
				}
				pending[name] = true
				command.onFailure = func(current *owner, _ error) {
					currentPending := current.taskPendingAgents[channelTask]
					if currentPending == nil {
						return
					}
					if hadEntry {
						currentPending[name] = wasSet
					} else {
						delete(currentPending, name)
					}
					if !wasMap && len(currentPending) == 0 {
						delete(current.taskPendingAgents, channelTask)
					}
				}
			}
		}
	}
	var reply core.Message
	var hasReply bool
	var applyChannel func(*comms.Router) error
	if parseErr == nil {
		if found {
			applyChannel = o.prepareAgentChannelApply(agentID, request, cause, contextTask)
		} else {
			reply, hasReply = o.agentReply(agentID, messageTask, text, cause)
			if hasReply {
				command.rejection = routingRequest{taskID: messageTask, action: "message", recipient: routingAgentLabel(o.workspace, reply.Recipients[0])}
			}
		}
	}
	ctx := o.ctx
	command.apply = func(router *comms.Router) error {
		if parseErr != nil {
			return router.RecordRejection(agentID, channelTask, parseErr.Error())
		}
		if found {
			return applyChannel(router)
		}
		if !hasReply {
			return nil
		}
		_, err := router.Send(ctx, reply)
		return err
	}
	o.enqueueBroker(command)
}

func (o *owner) agentReply(sender core.AgentID, taskID core.TaskID, text string, cause core.EventID) (core.Message, bool) {
	task, ok := o.workspace.task(taskID)
	if !ok || strings.TrimSpace(text) == "" {
		return core.Message{}, false
	}
	var replyTo *core.MessageID
	for _, message := range o.workspace.allMessages() {
		if message.TaskID == nil || *message.TaskID != taskID || message.Sender != task.Requester || !containsAgent(message.Recipients, sender) {
			continue
		}
		id := message.ID
		replyTo = &id
	}
	if replyTo == nil {
		return core.Message{}, false
	}
	id := task.ID
	return core.Message{
		Sender: sender, Recipients: []core.AgentID{task.Requester}, ThreadID: task.ThreadID,
		TaskID: &id, ReplyTo: replyTo, Type: core.MessageReply, Content: text, CausationID: cause,
	}, true
}

func (o *owner) openConsultationFor(agent core.AgentID) (comms.Consultation, bool) {
	if o.router == nil {
		return comms.Consultation{}, false
	}
	snapshot := o.router.Snapshot()
	// Per-agent delivery is FIFO. The oldest open request is the active reply;
	// its delivery may still be pending while the session emits that reply.
	for _, consultation := range snapshot.Consultations {
		if consultation.Status != comms.ConsultationOpen || consultation.Target != agent || consultation.RequestMessageID == "" {
			continue
		}
		for _, message := range snapshot.Messages {
			if message.ID != consultation.RequestMessageID || message.Type != core.MessageRequest || !containsAgent(message.Recipients, agent) {
				continue
			}
			if state, ok := message.RecipientDelivery[agent]; ok && state == core.DeliveryFailed {
				break
			}
			return consultation, true
		}
	}
	return comms.Consultation{}, false
}

func (o *owner) prepareAgentChannelApply(sender core.AgentID, request cognition.AgentChannelRequest, cause core.EventID, contextTask core.TaskID) func(*comms.Router) error {
	ctx := o.ctx
	controller := o.controller
	workspace := o.workspace
	workspaceID := workspace.workspaceID()
	memoryService := o.memory
	shareGlobalMemory := o.config.Memory.ShareGlobal
	delegationEnabled := o.delegationEnabled
	reject := func(taskID core.TaskID, reason string) func(*comms.Router) error {
		return func(router *comms.Router) error { return router.RecordRejection(sender, taskID, reason) }
	}

	switch request.Kind {
	case cognition.AgentChannelMessage:
		value := request.Message
		recipients, err := resolveChannelRecipients(workspace, value.Recipients, value.Type)
		if err != nil {
			return reject(core.TaskID(value.TaskID), err.Error())
		}
		kind := map[string]core.MessageType{
			"direct": core.MessageDirect, "broadcast": core.MessageBroadcast,
			"request": core.MessageRequest, "reply": core.MessageReply,
		}[value.Type]
		thread := core.ThreadID(value.ThreadID)
		var taskPointer *core.TaskID
		if value.TaskID != "" {
			id := core.TaskID(value.TaskID)
			taskPointer = &id
			if task, ok := workspace.task(id); ok && task.ThreadID != thread {
				return reject(id, "message thread does not match task")
			}
		}
		var replyTo *core.MessageID
		if value.ReplyTo != "" {
			id := core.MessageID(value.ReplyTo)
			replyTo = &id
		}
		message := core.Message{Sender: sender, Recipients: recipients, ThreadID: thread, TaskID: taskPointer, ReplyTo: replyTo, Type: kind, Content: value.Content, CausationID: cause}
		return func(router *comms.Router) error {
			_, err := router.Send(ctx, message)
			return err
		}
	case cognition.AgentChannelDelegate:
		value := request.Delegate
		if !delegationEnabled {
			return reject(core.TaskID(value.ParentTaskID), "new agent delegations are disabled by operator")
		}
		target, ok := workspace.agentID(value.Target)
		if !ok {
			return reject(core.TaskID(value.ParentTaskID), "unknown delegation target")
		}
		var policy *core.CESPolicy
		if value.CESPolicy != "" {
			parsed := core.CESPolicy(value.CESPolicy)
			policy = &parsed
		}
		if o.cesRequested == CESPolicyOn {
			if effective, _ := o.resolveCESPolicy(CESPolicyOn); effective == CESPolicyOn && (policy == nil || *policy != core.CESEnforce) {
				enforce := core.CESEnforce
				policy = &enforce
			}
		}
		var permissions *core.PermissionPolicy
		if value.Permissions != nil {
			permissions = &core.PermissionPolicy{Mode: core.PermissionMode(value.Permissions.Mode), AllowTools: append([]string(nil), value.Permissions.AllowTools...), DenyTools: append([]string(nil), value.Permissions.DenyTools...)}
		}
		var timeout time.Duration
		if value.Timeout != "" {
			var err error
			timeout, err = time.ParseDuration(value.Timeout)
			if err != nil {
				return reject(core.TaskID(value.ParentTaskID), "invalid delegation timeout")
			}
		}
		delegation := comms.DelegationRequest{
			Requester: sender, Target: target, ParentTaskID: core.TaskID(value.ParentTaskID),
			Objective: value.Objective, ContextRefs: append([]string(nil), value.ContextRefs...), ExpectedDeliverable: value.ExpectedDeliverable,
			CESPolicy: policy, PermissionPolicy: permissions, Timeout: timeout, CausationID: cause,
		}
		return func(router *comms.Router) error {
			_, err := router.Delegate(ctx, delegation)
			return err
		}
	case cognition.AgentChannelConsult:
		value := request.Consult
		target, ok := workspace.agentID(value.Target)
		if !ok {
			return reject(core.TaskID(value.ParentTaskID), "unknown consultation target")
		}
		consultation := comms.ConsultationRequest{
			Requester: sender, Target: target, ParentTaskID: core.TaskID(value.ParentTaskID),
			Question: value.Question, Evidence: append([]string(nil), value.Evidence...), References: append([]string(nil), value.References...), CausationID: cause,
		}
		return func(router *comms.Router) error {
			_, err := router.Consult(ctx, consultation)
			return err
		}
	case cognition.AgentChannelResult:
		value := request.Result
		id := core.TaskID(value.TaskID)
		failed, result := value.Status == "failed", value.Result
		return func(router *comms.Router) error {
			// Check ownership against the router, not the workspace projection:
			// a delegated child can reply before the broker sync publishes the
			// task the router just created and delivered to it.
			task, ok := router.Snapshot().Task(id)
			if !ok || task.Parent == nil || task.Owner != sender || task.Status.IsTerminal() {
				return router.RecordRejection(sender, id, "agent does not own an open delegated task")
			}
			if failed {
				_, err := router.Fail(ctx, id, result)
				return err
			}
			_, err := router.Complete(ctx, id, result)
			return err
		}
	case cognition.AgentChannelRecall:
		if memoryService == nil {
			return func(*comms.Router) error { return nil }
		}
		var task *core.Task
		thread := core.ThreadID("")
		if current, ok := workspace.task(contextTask); ok {
			task = &current
			thread = current.ThreadID
		}
		var taskID core.TaskID
		if task != nil {
			taskID = task.ID
		}
		recallRequest := core.MemoryRecallRequest{
			Query: request.Recall.Query, Requester: sender, Task: taskID,
			ParentTasks: memoryTaskParents(workspace, taskID),
			Scopes:      memoryRecallScopes(request.Recall.Scopes, shareGlobalMemory), Reason: "agent_recall",
		}
		return func(*comms.Router) error {
			result := memoryService.Recall(ctx, recallRequest)
			content := memoryDeliveryContent(result)
			return controller.call(func(current *owner) {
				current.addMemoryDelivery(memoryDelivery{
					agent: sender, task: taskID, thread: thread, content: content,
					recall: recallRequest, result: result,
				})
			})
		}
	case cognition.AgentChannelRemember:
		if memoryService == nil {
			return func(*comms.Router) error { return nil }
		}
		value := request.Remember
		claim := core.ClaimKind(value.Claim)
		text := value.Text
		if claim == core.ClaimInstruction {
			claim = core.ClaimObservation
			text = "[Agent instruction claim downgraded to observation] " + text
		}
		write := core.MemoryWrite{
			Scope: core.MemoryScope(value.Scope), Text: shortMemoryText(text, 4000),
			Provenance: core.MemoryProvenance{
				Agent: sender, Task: contextTask, Workspace: string(workspaceID), ClaimKind: claim,
				Validation: core.ValidationUnverified, Evidence: append([]string(nil), value.Evidence...),
				Counterevidence: append([]string(nil), value.Counterevidence...),
			},
		}
		return func(*comms.Router) error { memoryService.Enqueue(write); return nil }
	case cognition.AgentChannelChallenge:
		if memoryService == nil {
			return func(*comms.Router) error { return nil }
		}
		value := request.Challenge
		claim := core.ClaimKind(value.Claim)
		text := value.Text
		if claim == core.ClaimInstruction {
			claim = core.ClaimObservation
			text = "[Agent instruction claim downgraded to observation] " + text
		}
		write := core.MemoryWrite{
			Scope: core.MemoryScopeWorkspace, Text: shortMemoryText(text, 4000),
			Provenance: core.MemoryProvenance{
				Agent: sender, Task: contextTask, Workspace: string(workspaceID), ClaimKind: claim,
				Validation: core.ValidationUnverified, Evidence: append([]string(nil), value.Evidence...),
				Challenges: value.RecordID,
			},
		}
		return func(*comms.Router) error { memoryService.Enqueue(write); return nil }
	default:
		return reject("", "unsupported agent channel request")
	}
}

func resolveChannelRecipients(workspace *workspaceProjection, names []string, kind string) ([]core.AgentID, error) {
	if kind == "broadcast" {
		return []core.AgentID{core.BroadcastWildcard}, nil
	}
	ids := make([]core.AgentID, 0, len(names))
	for _, name := range names {
		id, ok := workspace.agentID(strings.TrimPrefix(name, "@"))
		if !ok {
			return nil, fmt.Errorf("unknown recipient %q", name)
		}
		ids = uniqueAgentIDs(ids, id)
	}
	return ids, nil
}

func (o *owner) taskForAgent(agent core.AgentID) core.TaskID {
	if id := o.agentTaskIDs[agent]; id != "" {
		return id
	}
	if task, ok := o.workspace.task(o.activeTaskID); ok && (task.Owner == agent || containsAgent(task.Participants, agent)) {
		return task.ID
	}
	return ""
}

func channelMessageTask(request cognition.AgentChannelRequest, fallback core.TaskID) core.TaskID {
	switch request.Kind {
	case cognition.AgentChannelMessage:
		if request.Message.TaskID != "" {
			return core.TaskID(request.Message.TaskID)
		}
	case cognition.AgentChannelDelegate:
		return core.TaskID(request.Delegate.ParentTaskID)
	case cognition.AgentChannelConsult:
		return core.TaskID(request.Consult.ParentTaskID)
	case cognition.AgentChannelResult:
		return core.TaskID(request.Result.TaskID)
	}
	return fallback
}

func routingRequestFor(request cognition.AgentChannelRequest, fallback core.TaskID, workspace *workspaceProjection, lookup taskLookup) routingRequest {
	routing := routingRequest{taskID: channelMessageTask(request, fallback)}
	switch request.Kind {
	case cognition.AgentChannelMessage:
		if request.Message == nil {
			return routingRequest{}
		}
		routing.action = "message"
		recipients := make([]string, 0, len(request.Message.Recipients))
		for _, recipient := range request.Message.Recipients {
			recipients = append(recipients, routingRecipientLabel(workspace, recipient))
		}
		routing.recipient = strings.Join(recipients, ", ")
	case cognition.AgentChannelDelegate:
		if request.Delegate == nil {
			return routingRequest{}
		}
		routing.action = "delegate"
		routing.recipient = routingRecipientLabel(workspace, request.Delegate.Target)
	case cognition.AgentChannelConsult:
		if request.Consult == nil {
			return routingRequest{}
		}
		routing.action = "consult"
		routing.recipient = routingRecipientLabel(workspace, request.Consult.Target)
	case cognition.AgentChannelResult:
		if request.Result == nil {
			return routingRequest{}
		}
		routing.action = "result"
		if task, ok := lookup(core.TaskID(request.Result.TaskID)); ok && task.Parent != nil {
			if parent, ok := lookup(*task.Parent); ok {
				routing.recipient = routingAgentLabel(workspace, parent.Owner)
			}
		}
		if routing.recipient == "" {
			routing.recipient = "owner of task " + request.Result.TaskID
		}
	default:
		return routingRequest{}
	}
	return routing
}

func routingRequestForParseError(parseErr error, fallback core.TaskID) routingRequest {
	routing := routingRequest{taskID: fallback, action: "channel"}
	if parseErr == nil {
		return routing
	}
	message := strings.TrimPrefix(parseErr.Error(), "agent channel ")
	if message == parseErr.Error() {
		return routing
	}
	kind, _, _ := strings.Cut(message, ":")
	if action, ok := strings.CutPrefix(kind, "ghost-"); ok {
		routing.action = action
	}
	return routing
}

func routingRecipientLabel(workspace *workspaceProjection, recipient string) string {
	if workspace != nil {
		if id, ok := workspace.agentID(recipient); ok {
			return routingAgentLabel(workspace, id)
		}
	}
	return strings.ToUpper(strings.TrimSpace(recipient))
}

func routingAgentLabel(workspace *workspaceProjection, id core.AgentID) string {
	if workspace != nil {
		if name, ok := workspace.agentName(id); ok {
			return strings.ToUpper(name)
		}
	}
	return strings.ToUpper(string(id))
}

// taskLookup resolves a task named by an agent channel request.
type taskLookup func(core.TaskID) (core.Task, bool)

// channelTaskLookup reads the workspace projection and, for ghost-result
// requests, falls back to the router's own state on a miss. The projection
// trails the router, so a child can reply before its task is projected.
func (o *owner) channelTaskLookup(request cognition.AgentChannelRequest) taskLookup {
	var snapshot *comms.Snapshot
	return func(id core.TaskID) (core.Task, bool) {
		if task, ok := o.workspace.task(id); ok {
			return task, true
		}
		if request.Kind != cognition.AgentChannelResult || o.router == nil {
			return core.Task{}, false
		}
		if snapshot == nil {
			current := o.router.Snapshot()
			snapshot = &current
		}
		return snapshot.Task(id)
	}
}

func pendingChannelTask(request cognition.AgentChannelRequest, fallback core.TaskID, lookup taskLookup) core.TaskID {
	if request.Kind == cognition.AgentChannelResult && request.Result != nil {
		if task, ok := lookup(core.TaskID(request.Result.TaskID)); ok && task.Parent != nil {
			parent := *task.Parent
			return parent
		}
		return fallback
	}
	return channelMessageTask(request, fallback)
}
