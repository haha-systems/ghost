package comms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

// Complete closes an open task and sends its result to the task requester.
func (r *Router) Complete(ctx context.Context, id core.TaskID, result string) (core.Task, error) {
	if strings.TrimSpace(result) == "" {
		return core.Task{}, errors.New("task result is required")
	}
	return r.finishTask(ctx, id, core.TaskCompleted, result, "", r.clock().UTC())
}

// Fail closes an open task, records the failure reason, and returns it to the
// requester as a task-result message.
func (r *Router) Fail(ctx context.Context, id core.TaskID, reason string) (core.Task, error) {
	if strings.TrimSpace(reason) == "" {
		return core.Task{}, errors.New("task failure reason is required")
	}
	return r.finishTask(ctx, id, core.TaskFailed, "failed: "+reason, reason, r.clock().UTC())
}

// SystemMessage queues a controller-authored notice through the normal
// delivery and projection path without exposing a human or agent sender.
func (r *Router) SystemMessage(ctx context.Context, recipient core.AgentID, thread core.ThreadID, taskID core.TaskID, content string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("system message content is required")
	}
	if thread == "" {
		id, err := r.ids.New("thread")
		if err != nil {
			return err
		}
		thread = core.ThreadID(id)
	}
	var task *core.TaskID
	if taskID != "" {
		id := taskID
		task = &id
	}
	message := core.Message{Sender: "", Recipients: []core.AgentID{recipient}, ThreadID: thread, TaskID: task, Type: core.MessageSystem, Content: content}
	accepted, acceptedEvent, err := r.acceptMessageAt(message, true, r.clock().UTC())
	if err != nil {
		return err
	}
	for _, id := range accepted.Recipients {
		if err := r.tryDeliver(ctx, accepted.ID, id, acceptedEvent); err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) finishTask(ctx context.Context, id core.TaskID, status core.TaskStatus, result, reason string, now time.Time) (core.Task, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	task, ok := r.tasks[id]
	if !ok {
		r.mu.Unlock()
		return core.Task{}, fmt.Errorf("unknown task %q", id)
	}
	if task.Status.IsTerminal() {
		r.mu.Unlock()
		return core.Task{}, fmt.Errorf("task %q is already terminal", id)
	}
	if status == core.TaskCompleted && task.Status == core.TaskQueued {
		if err := task.Transition(core.TaskRunning, now); err != nil {
			r.mu.Unlock()
			return core.Task{}, err
		}
	}
	if err := task.Transition(status, now); err != nil {
		r.mu.Unlock()
		return core.Task{}, err
	}
	task.FailureReason = reason
	cause := r.taskEvents[id]
	event, err := r.emitAtLocked(now, "task.status_changed", actorFor(task.Owner), id, task.Owner, task.ThreadID, task.CorrelationID, cause, map[string]any{"task_id": id, "status": status, "reason": reason})
	if err != nil {
		r.mu.Unlock()
		return core.Task{}, err
	}
	r.tasks[id] = cloneTask(task)
	r.taskEvents[id] = event.ID
	r.removeOpenRequestLocked(id)
	r.mu.Unlock()

	message := core.Message{
		Sender: task.Owner, Recipients: []core.AgentID{task.Requester},
		ThreadID: task.ThreadID, TaskID: &task.ID, Type: core.MessageTaskResult,
		Content: result, CorrelationID: task.CorrelationID, CausationID: event.ID,
	}
	if _, _, err := r.acceptMessageAt(message, true, now); err != nil {
		return task, fmt.Errorf("task closed but result message was not accepted: %w", err)
	}
	accepted := r.messageByTaskAndType(task.ID, core.MessageTaskResult)
	for _, recipient := range accepted.Recipients {
		if err := r.tryDeliver(ctx, accepted.ID, recipient, r.messageAcceptedID(accepted.ID)); err != nil {
			return task, err
		}
	}
	return r.task(id), nil
}

// Cancel cancels an open task and all open descendants. Each transition and
// requester notification is recorded by the broker.
func (r *Router) Cancel(ctx context.Context, id core.TaskID, reason string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("cancellation reason is required")
	}
	r.mu.Lock()
	if _, ok := r.tasks[id]; !ok {
		r.mu.Unlock()
		return fmt.Errorf("unknown task %q", id)
	}
	order := r.openDescendantsLocked(id)
	now := r.clock().UTC()
	cancelled := make([]core.Task, 0, len(order))
	causes := map[core.TaskID]core.EventID{}
	for _, taskID := range order {
		task := r.tasks[taskID]
		if task.Status.IsTerminal() {
			continue
		}
		cause := r.taskEvents[taskID]
		if task.Parent != nil && causes[*task.Parent] != "" {
			cause = causes[*task.Parent]
		}
		if err := task.Transition(core.TaskCancelled, now); err != nil {
			r.mu.Unlock()
			return err
		}
		task.FailureReason = reason
		event, err := r.emitAtLocked(now, "task.status_changed", core.Actor{Kind: core.ActorSystem, ID: "router"}, task.ID, task.Owner, task.ThreadID, task.CorrelationID, cause, map[string]any{"task_id": task.ID, "status": core.TaskCancelled, "reason": reason})
		if err != nil {
			r.mu.Unlock()
			return err
		}
		r.tasks[taskID] = cloneTask(task)
		r.taskEvents[taskID] = event.ID
		causes[taskID] = event.ID
		r.removeOpenRequestLocked(taskID)
		cancelled = append(cancelled, cloneTask(task))
	}
	r.cancelConsultationsLocked(order, reason, now, causes)
	r.mu.Unlock()

	for _, task := range cancelled {
		message := core.Message{
			Sender: task.Owner, Recipients: []core.AgentID{task.Requester},
			ThreadID: task.ThreadID, TaskID: &task.ID, Type: core.MessageTaskResult,
			Content: "cancelled: " + reason, CorrelationID: task.CorrelationID,
			CausationID: causes[task.ID],
		}
		accepted, acceptedEvent, err := r.acceptMessageAt(message, true, now)
		if err != nil {
			return fmt.Errorf("task %q cancelled but result message was not accepted: %w", task.ID, err)
		}
		for _, recipient := range accepted.Recipients {
			if err := r.tryDeliver(ctx, accepted.ID, recipient, acceptedEvent); err != nil {
				return err
			}
		}
	}
	return nil
}

// Tick marks expired tasks and pending deliveries. Task states remain in the
// core six-state set; timeout is recorded in Task.FailureReason.
func (r *Router) Tick(now time.Time) {
	if now.IsZero() {
		now = r.clock()
	}
	now = now.UTC()
	r.mu.Lock()
	var timedOut []core.TaskID
	for _, id := range r.taskOrder {
		task := r.tasks[id]
		deadline := task.Limits.Budget.Deadline
		if !task.Status.IsTerminal() && deadline != nil && !deadline.After(now) {
			timedOut = append(timedOut, id)
		}
	}
	type pendingRecipient struct {
		messageID core.MessageID
		recipient core.AgentID
		cause     core.EventID
	}
	var expired []pendingRecipient
	for _, id := range r.messageOrder {
		message := r.messages[id]
		if now.Sub(message.CreatedAt) < r.limits.MaxPendingAge {
			continue
		}
		for _, recipient := range sortedAgentIDs(message.Recipients) {
			if message.RecipientDelivery[recipient] == core.DeliveryPending {
				expired = append(expired, pendingRecipient{id, recipient, r.messageAccepted[id]})
			}
		}
	}
	failedDelegations := map[core.TaskID]string{}
	var failedOrder []core.TaskID
	for _, item := range expired {
		message := r.messages[item.messageID]
		if message.RecipientDelivery[item.recipient] != core.DeliveryPending {
			continue
		}
		reason := "recipient unavailable beyond maximum pending age"
		err := r.failRecipientAtLocked(&message, item.recipient, reason, item.cause, now)
		failedTask := delegationTask(message)
		if failedTask != "" && err == nil {
			r.taskEvents[failedTask] = r.events[len(r.events)-1].ID
			if _, already := failedDelegations[failedTask]; !already {
				failedOrder = append(failedOrder, failedTask)
			}
			failedDelegations[failedTask] = "delegation delivery failed: " + reason
		}
		r.messages[item.messageID] = cloneMessage(message)
	}
	r.expireConsultationsLocked(now)
	r.mu.Unlock()

	for _, id := range failedOrder {
		reason := failedDelegations[id]
		_, _ = r.finishTask(context.Background(), id, core.TaskFailed, "failed: "+reason, reason, now)
	}
	for _, id := range timedOut {
		if _, failedByDelivery := failedDelegations[id]; failedByDelivery {
			continue
		}
		_, _ = r.finishTask(context.Background(), id, core.TaskFailed, "failed: timeout", "timeout", now)
	}
}

func (r *Router) openDescendantsLocked(root core.TaskID) []core.TaskID {
	out := []core.TaskID{}
	seen := map[core.TaskID]bool{}
	var visit func(core.TaskID)
	visit = func(parent core.TaskID) {
		if seen[parent] {
			return
		}
		seen[parent] = true
		if task, ok := r.tasks[parent]; ok && !task.Status.IsTerminal() {
			out = append(out, parent)
		}
		for _, id := range r.taskOrder {
			if child := r.tasks[id]; child.Parent != nil && *child.Parent == parent && !child.Status.IsTerminal() {
				visit(id)
			}
		}
	}
	visit(root)
	return out
}

func (r *Router) removeOpenRequestLocked(id core.TaskID) {
	for key, taskID := range r.openRequests {
		if taskID == id {
			delete(r.openRequests, key)
		}
	}
}

func (r *Router) failRecipientAtLocked(message *core.Message, recipient core.AgentID, reason string, cause core.EventID, now time.Time) error {
	message.RecipientDelivery[recipient] = core.DeliveryFailed
	message.DeliveryReason[recipient] = reason
	message.Delivery = aggregateDelivery(message.RecipientDelivery)
	_, err := r.emitAtLocked(now, "message.failed", core.Actor{Kind: core.ActorSystem, ID: "router"}, taskID(message.TaskID), eventAgentID(recipient), message.ThreadID, message.CorrelationID, cause, map[string]any{"message_id": message.ID, "recipient": recipient, "state": core.DeliveryFailed, "reason": reason})
	return err
}
