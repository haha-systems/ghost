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
	request, found, parseErr := cognition.ParseAgentChannel(text)
	contextTask := o.taskForAgent(agentID)
	messageTask := channelMessageTask(request, contextTask)
	channelTask := pendingChannelTask(request, contextTask, o.workspace)
	if found || parseErr != nil {
		o.beginPendingChannel(channelTask)
	} else {
		o.beginPendingChannel(messageTask)
	}
	turnKey := item.SessionID + "/" + item.TurnID
	command := brokerCommand{notifyAgent: agentID, notifyTurn: turnKey}
	if found || parseErr != nil {
		command.pendingTaskID = channelTask
	} else {
		command.pendingTaskID = messageTask
	}
	if request.Kind == cognition.AgentChannelResult {
		if parent, ok := o.workspace.task(channelTask); ok {
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
	command.apply = func(router *comms.Router) error {
		if parseErr != nil {
			return router.RecordRejection(agentID, channelTask, parseErr.Error())
		}
		if found {
			return o.applyAgentChannel(router, agentID, request, cause, contextTask)
		}
		message, ok := o.agentReply(agentID, messageTask, text, cause)
		if !ok {
			return nil
		}
		_, err := router.Send(o.ctx, message)
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

func (o *owner) applyAgentChannel(router *comms.Router, sender core.AgentID, request cognition.AgentChannelRequest, cause core.EventID, contextTask core.TaskID) error {
	switch request.Kind {
	case cognition.AgentChannelMessage:
		value := request.Message
		recipients, err := o.resolveChannelRecipients(value.Recipients, value.Type)
		if err != nil {
			return router.RecordRejection(sender, core.TaskID(value.TaskID), err.Error())
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
			if task, ok := o.workspace.task(id); ok && task.ThreadID != thread {
				return router.RecordRejection(sender, id, "message thread does not match task")
			}
		}
		var replyTo *core.MessageID
		if value.ReplyTo != "" {
			id := core.MessageID(value.ReplyTo)
			replyTo = &id
		}
		_, err = router.Send(o.ctx, core.Message{Sender: sender, Recipients: recipients, ThreadID: thread, TaskID: taskPointer, ReplyTo: replyTo, Type: kind, Content: value.Content, CausationID: cause})
		return err
	case cognition.AgentChannelDelegate:
		value := request.Delegate
		if !o.delegationEnabled {
			return router.RecordRejection(sender, core.TaskID(value.ParentTaskID), "new agent delegations are disabled by operator")
		}
		target, ok := o.workspace.agentID(value.Target)
		if !ok {
			return router.RecordRejection(sender, core.TaskID(value.ParentTaskID), "unknown delegation target")
		}
		var policy *core.CESPolicy
		if value.CESPolicy != "" {
			parsed := core.CESPolicy(value.CESPolicy)
			policy = &parsed
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
				return router.RecordRejection(sender, core.TaskID(value.ParentTaskID), "invalid delegation timeout")
			}
		}
		_, err := router.Delegate(o.ctx, comms.DelegationRequest{
			Requester: sender, Target: target, ParentTaskID: core.TaskID(value.ParentTaskID),
			Objective: value.Objective, ContextRefs: value.ContextRefs, ExpectedDeliverable: value.ExpectedDeliverable,
			CESPolicy: policy, PermissionPolicy: permissions, Timeout: timeout, CausationID: cause,
		})
		return err
	case cognition.AgentChannelConsult:
		value := request.Consult
		target, ok := o.workspace.agentID(value.Target)
		if !ok {
			return router.RecordRejection(sender, core.TaskID(value.ParentTaskID), "unknown consultation target")
		}
		_, err := router.Consult(o.ctx, comms.ConsultationRequest{
			Requester: sender, Target: target, ParentTaskID: core.TaskID(value.ParentTaskID),
			Question: value.Question, Evidence: value.Evidence, References: value.References, CausationID: cause,
		})
		return err
	case cognition.AgentChannelResult:
		value := request.Result
		id := core.TaskID(value.TaskID)
		task, ok := o.workspace.task(id)
		if !ok || task.Parent == nil || task.Owner != sender || task.Status.IsTerminal() {
			return router.RecordRejection(sender, id, "agent does not own an open delegated task")
		}
		if value.Status == "failed" {
			_, err := router.Fail(o.ctx, id, value.Result)
			return err
		}
		_, err := router.Complete(o.ctx, id, value.Result)
		return err
	case cognition.AgentChannelRecall:
		if o.memory == nil {
			return nil
		}
		var task *core.Task
		thread := core.ThreadID("")
		if current, ok := o.workspace.task(contextTask); ok {
			task = &current
			thread = current.ThreadID
		}
		var taskID core.TaskID
		if task != nil {
			taskID = task.ID
		}
		result := memoryContext(o.memory, o.ctx, sender, taskID, memoryTaskParents(o.workspace, taskID), request.Recall.Query,
			"agent_recall", memoryRecallScopes(request.Recall.Scopes, o.config.Memory.ShareGlobal))
		content := memoryDeliveryContent(result)
		return o.controller.call(func(current *owner) {
			current.addMemoryDelivery(memoryDelivery{agent: sender, task: taskID, thread: thread, content: content})
		})
	case cognition.AgentChannelRemember:
		if o.memory == nil {
			return nil
		}
		value := request.Remember
		claim := core.ClaimKind(value.Claim)
		text := value.Text
		if claim == core.ClaimInstruction {
			claim = core.ClaimObservation
			text = "[Agent instruction claim downgraded to observation] " + text
		}
		o.enqueueMemoryWrite(core.MemoryWrite{
			Scope: core.MemoryScope(value.Scope), Text: shortMemoryText(text, 4000),
			Provenance: core.MemoryProvenance{
				Agent: sender, Task: contextTask, Workspace: string(o.workspace.workspaceID()), ClaimKind: claim,
				Validation: core.ValidationUnverified, Evidence: append([]string(nil), value.Evidence...),
				Counterevidence: append([]string(nil), value.Counterevidence...),
			},
		})
		return nil
	case cognition.AgentChannelChallenge:
		if o.memory == nil {
			return nil
		}
		value := request.Challenge
		claim := core.ClaimKind(value.Claim)
		text := value.Text
		if claim == core.ClaimInstruction {
			claim = core.ClaimObservation
			text = "[Agent instruction claim downgraded to observation] " + text
		}
		o.enqueueMemoryWrite(core.MemoryWrite{
			Scope: core.MemoryScopeWorkspace, Text: shortMemoryText(text, 4000),
			Provenance: core.MemoryProvenance{
				Agent: sender, Task: contextTask, Workspace: string(o.workspace.workspaceID()), ClaimKind: claim,
				Validation: core.ValidationUnverified, Evidence: append([]string(nil), value.Evidence...),
				Challenges: value.RecordID,
			},
		})
		return nil
	default:
		return router.RecordRejection(sender, "", "unsupported agent channel request")
	}
}

func (o *owner) resolveChannelRecipients(names []string, kind string) ([]core.AgentID, error) {
	if kind == "broadcast" {
		return []core.AgentID{core.BroadcastWildcard}, nil
	}
	ids := make([]core.AgentID, 0, len(names))
	for _, name := range names {
		id, ok := o.workspace.agentID(strings.TrimPrefix(name, "@"))
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

func pendingChannelTask(request cognition.AgentChannelRequest, fallback core.TaskID, workspace *workspaceProjection) core.TaskID {
	if request.Kind == cognition.AgentChannelResult && request.Result != nil {
		if task, ok := workspace.task(core.TaskID(request.Result.TaskID)); ok && task.Parent != nil {
			parent := *task.Parent
			return parent
		}
		return fallback
	}
	return channelMessageTask(request, fallback)
}
