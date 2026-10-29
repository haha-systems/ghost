package comms

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

const restartTaskReason = "interrupted: ghost restarted"
const uncertainDeliveryReason = "uncertain after restart"

// RecoveryResult reports changes made during restart reconciliation.
type RecoveryResult struct {
	ReconciledTasks     int
	UncertainDeliveries int
	PendingPureMessages int
}

type recoveryNotice struct {
	key, content string
	requester    core.AgentID
	thread       core.ThreadID
	task         core.TaskID
	correlation  core.EventID
}

// Rehydrate restores broker state from durable events. It does not call the
// delivery adapter or create new events.
func (r *Router) Rehydrate(events []core.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) != 0 || len(r.tasks) != 0 || len(r.messages) != 0 || len(r.consultations) != 0 {
		return fmt.Errorf("router state is not empty")
	}
	seen := map[core.EventID]bool{}
	lastSequences := map[string]uint64{}
	for _, item := range events {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("invalid replay event: %w", err)
		}
		if item.Workspace != r.workspaceID {
			return fmt.Errorf("event %s belongs to workspace %s", item.ID, item.Workspace)
		}
		if seen[item.ID] {
			return fmt.Errorf("duplicate replay event ID %s", item.ID)
		}
		seen[item.ID] = true
		if item.Seq > lastSequences[item.StreamKey()] {
			lastSequences[item.StreamKey()] = item.Seq
		}
		if item.ThreadID != "" && r.threadRoots[item.ThreadID] == "" {
			r.threadRoots[item.ThreadID] = item.CorrelationID
		}
		if item.Source == "comms" {
			r.events = append(r.events, cloneEvent(item))
		}
		if err := r.rehydrateEventLocked(item); err != nil {
			return fmt.Errorf("rehydrate event %s (%s): %w", item.ID, item.Type, err)
		}
	}
	for stream, sequence := range lastSequences {
		if err := r.sequencer.Restore(stream, sequence); err != nil {
			return err
		}
	}
	for _, id := range r.taskOrder {
		task := r.tasks[id]
		if task.Parent != nil && !task.Status.IsTerminal() {
			r.openRequests[delegationKey(task.Requester, task.Owner, *task.Parent, task.Objective)] = id
		}
	}
	return nil
}

func (r *Router) rehydrateEventLocked(item core.Event) error {
	switch item.Type {
	case "task.created":
		task, err := decodeCreatedTask(item.Payload)
		if err != nil {
			return err
		}
		if task.CorrelationID == "" {
			task.CorrelationID = item.CorrelationID
		}
		if err := task.Validate(); err != nil {
			return err
		}
		if _, exists := r.tasks[task.ID]; !exists {
			r.tasks[task.ID] = cloneTask(task)
			r.taskOrder = append(r.taskOrder, task.ID)
		}
		r.taskEvents[task.ID] = item.ID
		if task.ThreadID != "" && r.threadRoots[task.ThreadID] == "" {
			r.threadRoots[task.ThreadID] = task.CorrelationID
		}
	case "task.status_changed", "task.reconciled":
		var payload struct {
			TaskID core.TaskID     `json:"task_id"`
			Status core.TaskStatus `json:"status"`
			To     core.TaskStatus `json:"to"`
			Reason string          `json:"reason"`
			Task   *core.Task      `json:"task"`
		}
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err
		}
		if payload.Task != nil {
			_, exists := r.tasks[payload.Task.ID]
			r.tasks[payload.Task.ID] = cloneTask(*payload.Task)
			payload.TaskID = payload.Task.ID
			if !exists {
				r.taskOrder = append(r.taskOrder, payload.TaskID)
			}
		}
		status := payload.Status
		if status == "" {
			status = payload.To
		}
		if payload.TaskID != "" && status != "" {
			if task, ok := r.tasks[payload.TaskID]; ok {
				if err := applyReplayedTaskStatus(&task, status, payload.Reason, item.Time); err != nil {
					return err
				}
				r.tasks[payload.TaskID] = cloneTask(task)
				if task.Status.IsTerminal() {
					r.removeOpenRequestLocked(task.ID)
				}
			}
		}
		if payload.TaskID != "" {
			r.taskEvents[payload.TaskID] = item.ID
		}
	case "task.owner_changed":
		var payload struct {
			TaskID core.TaskID  `json:"task_id"`
			To     core.AgentID `json:"to"`
		}
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err
		}
		if task, ok := r.tasks[payload.TaskID]; ok && payload.To != "" {
			applyTaskOwnerChange(&task, payload.To, item.Time)
			r.tasks[task.ID] = cloneTask(task)
			r.taskEvents[task.ID] = item.ID
		}
	case "message.accepted":
		var payload struct {
			Message core.Message `json:"message"`
		}
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err
		}
		message := payload.Message
		if err := message.Validate(); err != nil {
			return err
		}
		if message.RecipientDelivery == nil {
			message.RecipientDelivery = make(map[core.AgentID]core.DeliveryStatus, len(message.Recipients))
			for _, recipient := range message.Recipients {
				message.RecipientDelivery[recipient] = core.DeliveryPending
			}
		}
		if message.DeliveryReason == nil {
			message.DeliveryReason = map[core.AgentID]string{}
		}
		if _, exists := r.messages[message.ID]; !exists {
			r.messages[message.ID] = cloneMessage(message)
			r.messageOrder = append(r.messageOrder, message.ID)
		}
		r.messageAccepted[message.ID] = item.ID
		if message.TaskID != nil && message.Type != core.MessageTaskDelegation && message.Type != core.MessageTaskResult && message.Type != core.MessageSystem {
			r.taskMessages[*message.TaskID]++
		}
		if message.ThreadID != "" && r.threadRoots[message.ThreadID] == "" {
			r.threadRoots[message.ThreadID] = message.CorrelationID
		}
		r.linkConsultationMessageLocked(message)
	case "message.delivered", "message.failed":
		var payload struct {
			MessageID core.MessageID      `json:"message_id"`
			Recipient core.AgentID        `json:"recipient"`
			State     core.DeliveryStatus `json:"state"`
			Reason    string              `json:"reason"`
		}
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err
		}
		if message, ok := r.messages[payload.MessageID]; ok {
			if message.RecipientDelivery == nil {
				message.RecipientDelivery = map[core.AgentID]core.DeliveryStatus{}
			}
			message.RecipientDelivery[payload.Recipient] = payload.State
			if payload.State == core.DeliveryFailed && payload.Reason != "" {
				if message.DeliveryReason == nil {
					message.DeliveryReason = map[core.AgentID]string{}
				}
				message.DeliveryReason[payload.Recipient] = payload.Reason
			}
			message.Delivery = aggregateDelivery(message.RecipientDelivery)
			r.messages[message.ID] = cloneMessage(message)
			if payload.State != core.DeliveryPending {
				r.deliveryAttempt[deliveryKey(message.ID, payload.Recipient)] = true
			}
		}
	case "coordination.consultation_requested":
		var payload struct {
			Consultation Consultation `json:"consultation"`
		}
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err
		}
		consultation := payload.Consultation
		if consultation.CorrelationID == "" {
			consultation.CorrelationID = item.CorrelationID
		}
		if _, exists := r.consultations[consultation.ID]; !exists {
			r.consultations[consultation.ID] = cloneConsultation(consultation)
			r.consultOrder = append(r.consultOrder, consultation.ID)
		}
	case "coordination.consultation_completed", "coordination.consultation_failed", "coordination.consultation_timed_out", "coordination.consultation_cancelled":
		var payload struct {
			ID     core.ConsultationID `json:"consultation_id"`
			Result *ConsultationResult `json:"result"`
			Reason string              `json:"reason"`
		}
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err
		}
		if consultation, ok := r.consultations[payload.ID]; ok {
			switch item.Type {
			case "coordination.consultation_completed":
				consultation.Status, consultation.Result = ConsultationCompleted, payload.Result
			case "coordination.consultation_failed":
				consultation.Status, consultation.FailureReason = ConsultationFailed, payload.Reason
			case "coordination.consultation_timed_out":
				consultation.Status, consultation.FailureReason = ConsultationTimedOut, payload.Reason
			case "coordination.consultation_cancelled":
				consultation.Status, consultation.FailureReason = ConsultationCancelled, payload.Reason
			}
			consultation.CausationID = item.ID
			r.consultations[payload.ID] = cloneConsultation(consultation)
		}
	}
	return nil
}

func decodeCreatedTask(data []byte) (core.Task, error) {
	var wrapped struct {
		Task *core.Task `json:"task"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return core.Task{}, err
	}
	if wrapped.Task != nil {
		return *wrapped.Task, nil
	}
	var task core.Task
	if err := json.Unmarshal(data, &task); err != nil {
		return core.Task{}, err
	}
	return task, nil
}

func applyReplayedTaskStatus(task *core.Task, status core.TaskStatus, reason string, at time.Time) error {
	if task.Status == status {
		task.FailureReason = reason
		return nil
	}
	if task.Status.IsTerminal() {
		return nil
	}
	if at.Before(task.UpdatedAt) {
		at = task.UpdatedAt
	}
	if status == core.TaskCompleted && task.Status == core.TaskQueued {
		if err := task.Transition(core.TaskRunning, at); err != nil {
			return err
		}
	}
	if !core.CanTransitionTask(task.Status, status) {
		return fmt.Errorf("invalid replayed task transition %s -> %s", task.Status, status)
	}
	if err := task.Transition(status, at); err != nil {
		return err
	}
	task.FailureReason = reason
	return nil
}

func (r *Router) linkConsultationMessageLocked(message core.Message) {
	if message.Type != core.MessageRequest {
		return
	}
	var payload struct {
		ConsultationID core.ConsultationID `json:"consultation_id"`
	}
	if json.Unmarshal([]byte(message.Content), &payload) != nil || payload.ConsultationID == "" {
		return
	}
	consultation, ok := r.consultations[payload.ConsultationID]
	if ok {
		consultation.RequestMessageID = message.ID
		r.consultations[consultation.ID] = cloneConsultation(consultation)
	}
}

// ReconcileAfterRestart closes unfinished work and classifies pending
// deliveries. It never retries a request, delegation, or task result.
func (r *Router) ReconcileAfterRestart(_ context.Context) (RecoveryResult, error) {
	var result RecoveryResult
	notices := map[string]recoveryNotice{}
	r.mu.Lock()
	now := r.clock().UTC()
	consultByMessage := map[core.MessageID]Consultation{}
	for _, id := range r.consultOrder {
		consultation := r.consultations[id]
		if consultation.RequestMessageID != "" {
			consultByMessage[consultation.RequestMessageID] = consultation
		}
	}
	for _, id := range r.messageOrder {
		message := r.messages[id]
		pendingPure := false
		for _, recipient := range message.Recipients {
			if message.RecipientDelivery[recipient] != core.DeliveryPending {
				continue
			}
			if isPureRecoveryMessage(message.Type) {
				pendingPure = true
				continue
			}
			if err := r.failRecipientLocked(&message, recipient, uncertainDeliveryReason, r.messageAccepted[id]); err != nil {
				r.mu.Unlock()
				return result, err
			}
			result.UncertainDeliveries++
			r.messages[id] = cloneMessage(message)
			n := r.noticeForUncertainLocked(message, consultByMessage[message.ID])
			if n.requester != "" {
				notices[n.key] = n
			}
			if childID := delegationTask(message); childID != "" {
				if child, ok := r.tasks[childID]; ok {
					if _, exists := notices["task:"+string(child.ID)]; !exists {
						notices["task:"+string(child.ID)] = recoveryNotice{
							key: "task:" + string(child.ID), requester: child.Requester, thread: child.ThreadID,
							task: child.ID, correlation: child.CorrelationID,
							content: "Ghost restarted before the delegation outcome was recorded. The task was stopped and was not sent again.",
						}
					}
				}
			}
		}
		if pendingPure {
			result.PendingPureMessages++
		}
	}
	for _, id := range r.consultOrder {
		consultation := r.consultations[id]
		if consultation.Status != ConsultationOpen {
			continue
		}
		consultation.Status = ConsultationFailed
		consultation.FailureReason = uncertainDeliveryReason
		event, err := r.emitAtLocked(now, "coordination.consultation_failed", core.Actor{Kind: core.ActorSystem, ID: "router"}, consultation.ParentTaskID, consultation.Target, "", consultation.CorrelationID, consultation.CausationID, map[string]any{"consultation_id": id, "reason": uncertainDeliveryReason})
		if err != nil {
			r.mu.Unlock()
			return result, err
		}
		consultation.CausationID = event.ID
		r.consultations[id] = cloneConsultation(consultation)
		key := "consultation:" + string(id)
		notices[key] = recoveryNotice{key: key, requester: consultation.Requester, thread: r.consultationThreadLocked(consultation), task: consultation.ParentTaskID, correlation: consultation.CorrelationID,
			content: "Ghost restarted before the consultation result was recorded. The request was stopped and was not sent again."}
	}
	interrupted := map[core.TaskID]bool{}
	for _, id := range r.taskOrder {
		task := r.tasks[id]
		if task.Status.IsTerminal() {
			continue
		}
		cascade := task.Parent != nil && interrupted[*task.Parent]
		at := now
		if at.Before(task.UpdatedAt) {
			at = task.UpdatedAt
		}
		from := task.Status
		if err := task.Transition(core.TaskFailed, at); err != nil {
			r.mu.Unlock()
			return result, err
		}
		task.FailureReason = restartTaskReason
		event, err := r.emitAtLocked(at, "task.reconciled", core.Actor{Kind: core.ActorSystem, ID: "router"}, task.ID, task.Owner, task.ThreadID, task.CorrelationID, r.taskEvents[task.ID], map[string]any{
			"task_id": task.ID, "from": from, "to": core.TaskFailed, "status": core.TaskFailed,
			"reason": restartTaskReason, "cascade": cascade, "task": task,
		})
		if err != nil {
			r.mu.Unlock()
			return result, err
		}
		r.tasks[id] = cloneTask(task)
		r.taskEvents[id] = event.ID
		r.removeOpenRequestLocked(id)
		interrupted[id] = true
		result.ReconciledTasks++
	}
	r.mu.Unlock()

	noticeKeys := make([]string, 0, len(notices))
	for key := range notices {
		noticeKeys = append(noticeKeys, key)
	}
	sort.Strings(noticeKeys)
	for _, key := range noticeKeys {
		n := notices[key]
		if n.requester == "" {
			continue
		}
		thread := n.thread
		if thread == "" {
			id, err := r.ids.New("thread")
			if err != nil {
				return result, err
			}
			thread = core.ThreadID(id)
		}
		var taskID *core.TaskID
		if n.task != "" {
			id := n.task
			taskID = &id
		}
		message := core.Message{Sender: "", Recipients: []core.AgentID{n.requester}, ThreadID: thread, TaskID: taskID, Type: core.MessageSystem, Content: n.content, CorrelationID: n.correlation}
		accepted, _, err := r.acceptMessageAt(message, true, now)
		if err != nil {
			return result, err
		}
		if n.requester == core.HumanAgentID {
			if err := r.markRecoveryNoticeRead(accepted.ID, core.HumanAgentID, now); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func isPureRecoveryMessage(kind core.MessageType) bool {
	switch kind {
	case core.MessageDirect, core.MessageBroadcast, core.MessageReply, core.MessageSystem:
		return true
	default:
		return false
	}
}

func (r *Router) noticeForUncertainLocked(message core.Message, consultation Consultation) recoveryNotice {
	if consultation.ID != "" {
		key := "consultation:" + string(consultation.ID)
		return recoveryNotice{key: key, requester: consultation.Requester, thread: message.ThreadID, task: consultation.ParentTaskID, correlation: consultation.CorrelationID,
			content: "Ghost restarted before the consultation delivery outcome was recorded. The request was stopped and was not sent again."}
	}
	if childID := delegationTask(message); childID != "" {
		if child, ok := r.tasks[childID]; ok {
			key := "task:" + string(child.ID)
			return recoveryNotice{key: key, requester: child.Requester, thread: child.ThreadID, task: child.ID, correlation: child.CorrelationID,
				content: "Ghost restarted before the delegation outcome was recorded. The task was stopped and was not sent again."}
		}
	}
	key := "message:" + string(message.ID)
	return recoveryNotice{key: key, requester: message.Sender, thread: message.ThreadID, task: taskIDOf(message.TaskID), correlation: message.CorrelationID,
		content: "Ghost restarted before the delivery outcome was recorded. The message was stopped and was not sent again."}
}

func (r *Router) consultationThreadLocked(consultation Consultation) core.ThreadID {
	if consultation.ParentTaskID != "" {
		if task, ok := r.tasks[consultation.ParentTaskID]; ok {
			return task.ThreadID
		}
	}
	if message, ok := r.messages[consultation.RequestMessageID]; ok {
		return message.ThreadID
	}
	return ""
}

func (r *Router) markRecoveryNoticeRead(id core.MessageID, recipient core.AgentID, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	message, ok := r.messages[id]
	if !ok || message.RecipientDelivery[recipient] != core.DeliveryPending {
		return nil
	}
	message.RecipientDelivery[recipient] = core.DeliveryDelivered
	message.Delivery = aggregateDelivery(message.RecipientDelivery)
	if _, err := r.emitAtLocked(at, "message.delivered", core.Actor{Kind: core.ActorSystem, ID: "router"}, taskID(message.TaskID), "", message.ThreadID, message.CorrelationID, r.messageAccepted[id], map[string]any{"message_id": id, "recipient": recipient, "state": core.DeliveryDelivered}); err != nil {
		return err
	}
	r.messages[id] = cloneMessage(message)
	return nil
}
