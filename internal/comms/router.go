package comms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

type Router struct {
	mu          sync.Mutex
	workspaceID core.WorkspaceID
	clock       func() time.Time
	ids         *core.IDGenerator
	directory   AgentDirectory
	delivery    Delivery
	limits      Limits
	sequencer   *core.Sequencer

	messages        map[core.MessageID]core.Message
	messageOrder    []core.MessageID
	messageAccepted map[core.MessageID]core.EventID
	deliveryAttempt map[string]bool
	tasks           map[core.TaskID]core.Task
	taskOrder       []core.TaskID
	taskEvents      map[core.TaskID]core.EventID
	taskMessages    map[core.TaskID]int
	openRequests    map[string]core.TaskID
	consultations   map[core.ConsultationID]Consultation
	consultOrder    []core.ConsultationID
	events          []core.Event
	threadRoots     map[core.ThreadID]core.EventID
}

func NewRouter(config RouterConfig) (*Router, error) {
	if err := config.WorkspaceID.Validate(); err != nil {
		return nil, fmt.Errorf("workspace ID: %w", err)
	}
	if config.Directory == nil {
		return nil, errors.New("agent directory is required")
	}
	if config.Delivery == nil {
		return nil, errors.New("delivery transport is required")
	}
	limits, err := config.Limits.withDefaults()
	if err != nil {
		return nil, err
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	ids := config.IDs
	if ids == nil {
		ids = core.NewIDGenerator(clock, nil)
	}
	return &Router{
		workspaceID:     config.WorkspaceID,
		clock:           clock,
		ids:             ids,
		directory:       config.Directory,
		delivery:        config.Delivery,
		limits:          limits,
		sequencer:       core.NewSequencer(),
		messages:        map[core.MessageID]core.Message{},
		messageAccepted: map[core.MessageID]core.EventID{},
		deliveryAttempt: map[string]bool{},
		tasks:           map[core.TaskID]core.Task{},
		taskEvents:      map[core.TaskID]core.EventID{},
		taskMessages:    map[core.TaskID]int{},
		openRequests:    map[string]core.TaskID{},
		consultations:   map[core.ConsultationID]Consultation{},
		threadRoots:     map[core.ThreadID]core.EventID{},
	}, nil
}

// RegisterTask adds a task that was created outside the broker. It is the
// entry point for a later controller adapter to register parent tasks.
func (r *Router) RegisterTask(task core.Task) error {
	if err := task.Validate(); err != nil {
		return fmt.Errorf("register task: %w", err)
	}
	if _, ok := r.directory.Agent(task.Owner); !ok {
		return fmt.Errorf("unknown task owner %q", task.Owner)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tasks[task.ID]; exists {
		return fmt.Errorf("task %q already exists", task.ID)
	}
	if task.Parent != nil {
		if _, exists := r.tasks[*task.Parent]; !exists {
			return fmt.Errorf("unknown parent task %q", *task.Parent)
		}
	}
	now := r.clock().UTC()
	if task.CorrelationID == "" && task.Parent != nil {
		if parent, ok := r.tasks[*task.Parent]; ok {
			task.CorrelationID = parent.CorrelationID
		}
	}
	event, err := r.emitLocked("task.created", actorFor(task.Requester), task.ID, task.Owner, task.ThreadID, task.CorrelationID, "", task)
	if err != nil {
		return err
	}
	if task.CorrelationID == "" {
		task.CorrelationID = event.CorrelationID
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	r.tasks[task.ID] = cloneTask(task)
	r.taskOrder = append(r.taskOrder, task.ID)
	r.taskEvents[task.ID] = event.ID
	if task.ThreadID != "" && r.threadRoots[task.ThreadID] == "" {
		r.threadRoots[task.ThreadID] = task.CorrelationID
	}
	return nil
}

// Send validates, stores, and routes one logical message. A broadcast keeps
// one message ID and records a separate state for each recipient.
func (r *Router) Send(ctx context.Context, message core.Message) (core.Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	accepted, acceptedEvent, err := r.acceptMessage(message, false)
	if err != nil {
		return core.Message{}, err
	}
	for _, recipient := range accepted.Recipients {
		if err := r.tryDeliver(ctx, accepted.ID, recipient, acceptedEvent); err != nil {
			return r.message(accepted.ID), err
		}
	}
	return r.message(accepted.ID), nil
}

func (r *Router) acceptMessage(message core.Message, brokerOwned bool) (core.Message, core.EventID, error) {
	return r.acceptMessageAt(message, brokerOwned, r.clock().UTC())
}

func (r *Router) acceptMessageAt(message core.Message, brokerOwned bool, now time.Time) (core.Message, core.EventID, error) {
	id, err := r.ids.New("message")
	if err != nil {
		return core.Message{}, "", err
	}
	message.ID = core.MessageID(id)
	message.CreatedAt = now
	message.Delivery = core.DeliveryPending
	message.RecipientDelivery = nil
	message.DeliveryReason = nil
	if message.Sender != core.HumanAgentID && !(message.Type == core.MessageSystem && message.Sender == "") {
		if _, ok := r.directory.Agent(message.Sender); !ok {
			return core.Message{}, "", r.reject(message.Sender, message.ThreadID, message.TaskID, "unknown sender", map[string]any{"message_id": message.ID})
		}
	}
	if message.Type == core.MessageBroadcast && (len(message.Recipients) == 0 || (len(message.Recipients) == 1 && message.Recipients[0] == core.BroadcastWildcard)) {
		message.Recipients = message.Recipients[:0]
		for _, agent := range r.directory.Agents() {
			if agent.ID != message.Sender {
				message.Recipients = append(message.Recipients, agent.ID)
			}
		}
		sort.Slice(message.Recipients, func(i, j int) bool { return message.Recipients[i] < message.Recipients[j] })
	}
	if len(message.Recipients) == 0 {
		return core.Message{}, "", r.reject(message.Sender, message.ThreadID, message.TaskID, "message has no recipients", map[string]any{"message_id": message.ID})
	}
	for _, recipient := range message.Recipients {
		if recipient == core.HumanAgentID {
			continue
		}
		if _, ok := r.directory.Agent(recipient); !ok {
			return core.Message{}, "", r.reject(message.Sender, message.ThreadID, message.TaskID, "unknown recipient", map[string]any{"message_id": message.ID, "recipient": recipient})
		}
		if !brokerOwned && !r.directory.CanMessage(message.Sender, recipient) {
			return core.Message{}, "", r.reject(message.Sender, message.ThreadID, message.TaskID, "message permission denied", map[string]any{"message_id": message.ID, "recipient": recipient})
		}
	}
	message.RecipientDelivery = make(map[core.AgentID]core.DeliveryStatus, len(message.Recipients))
	message.DeliveryReason = make(map[core.AgentID]string)
	for _, recipient := range message.Recipients {
		message.RecipientDelivery[recipient] = core.DeliveryPending
	}
	if err := message.Validate(); err != nil {
		return core.Message{}, "", r.reject(message.Sender, message.ThreadID, message.TaskID, err.Error(), map[string]any{"message_id": message.ID})
	}
	r.mu.Lock()
	if message.TaskID != nil {
		task, ok := r.tasks[*message.TaskID]
		if !ok {
			r.mu.Unlock()
			return core.Message{}, "", r.reject(message.Sender, message.ThreadID, message.TaskID, "unknown task", map[string]any{"message_id": message.ID})
		}
		if !brokerOwned && message.Sender != core.HumanAgentID && message.Sender != task.Owner && message.Sender != task.Requester && !containsAgent(task.Participants, message.Sender) {
			r.mu.Unlock()
			return core.Message{}, "", r.reject(message.Sender, message.ThreadID, message.TaskID, "sender is not part of task", map[string]any{"message_id": message.ID})
		}
		budget := r.limits.MaxMessagesPerTask
		if task.Limits.MaxMessages > 0 && task.Limits.MaxMessages < budget {
			budget = task.Limits.MaxMessages
		}
		if !brokerOwned && r.taskMessages[*message.TaskID] >= budget {
			r.mu.Unlock()
			return core.Message{}, "", r.reject(message.Sender, message.ThreadID, message.TaskID, "per-task message budget exhausted", map[string]any{"message_id": message.ID, "budget": budget})
		}
	}
	correlation := message.CorrelationID
	if correlation == "" && message.TaskID != nil {
		correlation = r.tasks[*message.TaskID].CorrelationID
	}
	if correlation == "" {
		correlation = r.threadRoots[message.ThreadID]
	}
	message.CorrelationID = correlation
	if err := r.checkUniqueIDLocked(message.ID); err != nil {
		r.mu.Unlock()
		return core.Message{}, "", err
	}
	accepted, err := r.emitLocked("message.accepted", actorFor(message.Sender), taskID(message.TaskID), firstRecipient(message.Recipients), message.ThreadID, correlation, message.CausationID, map[string]any{"message": message})
	if err != nil {
		r.mu.Unlock()
		return core.Message{}, "", err
	}
	if message.CorrelationID == "" {
		message.CorrelationID = accepted.CorrelationID
	}
	message.CausationID = accepted.ID
	r.messages[message.ID] = cloneMessage(message)
	r.messageOrder = append(r.messageOrder, message.ID)
	r.messageAccepted[message.ID] = accepted.ID
	if message.ThreadID != "" && r.threadRoots[message.ThreadID] == "" {
		r.threadRoots[message.ThreadID] = message.CorrelationID
	}
	if message.TaskID != nil && !brokerOwned {
		r.taskMessages[*message.TaskID]++
	}
	r.mu.Unlock()
	return message, accepted.ID, nil
}

func (r *Router) tryDeliver(ctx context.Context, messageID core.MessageID, recipient core.AgentID, cause core.EventID) error {
	r.mu.Lock()
	message, ok := r.messages[messageID]
	if !ok || message.RecipientDelivery[recipient] != core.DeliveryPending {
		r.mu.Unlock()
		return nil
	}
	key := deliveryKey(messageID, recipient)
	if r.deliveryAttempt[key] {
		r.mu.Unlock()
		return nil
	}
	if r.clock().Sub(message.CreatedAt) >= r.limits.MaxPendingAge {
		reason := "recipient unavailable beyond maximum pending age"
		err := r.failRecipientLocked(&message, recipient, reason, cause)
		failedTask := delegationTask(message)
		if failedTask != "" && err == nil && len(r.events) > 0 {
			r.taskEvents[failedTask] = r.events[len(r.events)-1].ID
		}
		r.messages[messageID] = cloneMessage(message)
		r.mu.Unlock()
		if failedTask != "" {
			_, failErr := r.finishTask(ctx, failedTask, core.TaskFailed, "failed: delegation delivery failed: "+reason, "delegation delivery failed: "+reason, r.clock().UTC())
			if failErr != nil {
				return failErr
			}
		}
		return err
	}
	available := recipient == core.HumanAgentID
	if !available {
		if agent, exists := r.directory.Agent(recipient); exists {
			available = agent.Availability == core.AvailabilityAvailable
		}
	}
	if !available {
		r.mu.Unlock()
		return nil
	}
	r.deliveryAttempt[key] = true
	messageCopy := cloneMessage(message)
	var taskCopy core.Task
	hasTask := false
	if message.Type == core.MessageTaskDelegation && message.TaskID != nil {
		if task, exists := r.tasks[*message.TaskID]; exists {
			taskCopy, hasTask = cloneTask(task), true
		}
	}
	r.mu.Unlock()

	var deliveryErr error
	if asyncDelivery, ok := r.delivery.(AsyncDelivery); ok && recipient != core.HumanAgentID {
		var task *core.Task
		if hasTask {
			task = &taskCopy
		}
		if err := asyncDelivery.QueueDelivery(ctx, recipient, messageCopy, task, func(err error) error {
			return r.completeDelivery(ctx, messageID, recipient, cause, err)
		}); err == nil {
			return nil
		} else {
			deliveryErr = err
		}
	} else if taskDelivery, ok := r.delivery.(TaskDelivery); ok && hasTask {
		deliveryErr = taskDelivery.DeliverTask(ctx, recipient, messageCopy, taskCopy)
	} else {
		deliveryErr = r.delivery.Deliver(ctx, recipient, messageCopy)
	}
	return r.completeDelivery(ctx, messageID, recipient, cause, deliveryErr)
}

func (r *Router) completeDelivery(ctx context.Context, messageID core.MessageID, recipient core.AgentID, cause core.EventID, deliveryErr error) error {
	r.mu.Lock()
	message, ok := r.messages[messageID]
	if !ok || message.RecipientDelivery[recipient] != core.DeliveryPending {
		r.mu.Unlock()
		return nil
	}
	var failedTask core.TaskID
	var failureReason string
	var eventErr error
	if deliveryErr != nil {
		failureReason = "delivery error: " + deliveryErr.Error()
		eventErr = r.failRecipientLocked(&message, recipient, failureReason, cause)
		failedTask = delegationTask(message)
		if failedTask != "" && eventErr == nil && len(r.events) > 0 {
			r.taskEvents[failedTask] = r.events[len(r.events)-1].ID
		}
	} else {
		message.RecipientDelivery[recipient] = core.DeliveryDelivered
		message.Delivery = aggregateDelivery(message.RecipientDelivery)
		_, eventErr = r.emitLocked("message.delivered", core.Actor{Kind: core.ActorSystem, ID: "router"}, taskID(message.TaskID), eventAgentID(recipient), message.ThreadID, message.CorrelationID, cause, map[string]any{"message_id": message.ID, "recipient": recipient, "state": core.DeliveryDelivered})
	}
	r.messages[messageID] = cloneMessage(message)
	r.mu.Unlock()
	if eventErr != nil {
		return eventErr
	}
	if failedTask != "" {
		_, err := r.finishTask(ctx, failedTask, core.TaskFailed, "failed: delegation delivery failed: "+failureReason, "delegation delivery failed: "+failureReason, r.clock().UTC())
		return err
	}
	return nil
}

// AgentUnavailable fails pending deliveries after a session start failure or
// another permanent transport failure. Transient absence remains pending
// until AgentAvailable retries it.
func (r *Router) AgentUnavailable(ctx context.Context, id core.AgentID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "agent session is unavailable"
	}
	var errs []error
	for _, message := range r.Snapshot().Messages {
		if message.RecipientDelivery[id] != core.DeliveryPending {
			continue
		}
		cause := r.messageAcceptedID(message.ID)
		var child core.TaskID
		r.mu.Lock()
		current, ok := r.messages[message.ID]
		if ok && current.RecipientDelivery[id] == core.DeliveryPending {
			if err := r.failRecipientLocked(&current, id, "delivery error: "+reason, cause); err != nil {
				errs = append(errs, err)
			}
			child = delegationTask(current)
			r.messages[message.ID] = cloneMessage(current)
		}
		r.mu.Unlock()
		if child != "" {
			if _, err := r.Fail(ctx, child, "delegation delivery failed: "+reason); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// TransitionTask updates a controller-owned task without creating a result
// message. It keeps router authorization in step with the run controller.
func (r *Router) TransitionTask(id core.TaskID, status core.TaskStatus, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("unknown task %q", id)
	}
	if task.Status == status {
		return nil
	}
	if task.Status.IsTerminal() {
		return fmt.Errorf("task %q is already terminal", id)
	}
	now := r.clock().UTC()
	if err := task.Transition(status, now); err != nil {
		return err
	}
	task.FailureReason = reason
	prior := r.taskEvents[id]
	event, err := r.emitAtLocked(now, "task.status_changed", core.Actor{Kind: core.ActorSystem, ID: "controller"}, id, task.Owner, task.ThreadID, task.CorrelationID, prior, map[string]any{"task_id": id, "status": status, "reason": reason})
	if err != nil {
		return err
	}
	r.tasks[id] = cloneTask(task)
	r.taskEvents[id] = event.ID
	r.removeOpenRequestLocked(id)
	return nil
}

// RecordRejection appends a visible rejection for malformed channel requests
// that the parser cannot pass to a normal broker operation.
func (r *Router) RecordRejection(sender core.AgentID, task core.TaskID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "invalid agent channel request"
	}
	return r.reject(sender, "", taskIDPtr(task), reason, nil)
}

func (r *Router) failRecipientLocked(message *core.Message, recipient core.AgentID, reason string, cause core.EventID) error {
	message.RecipientDelivery[recipient] = core.DeliveryFailed
	message.DeliveryReason[recipient] = reason
	message.Delivery = aggregateDelivery(message.RecipientDelivery)
	if _, err := r.emitLocked("message.failed", core.Actor{Kind: core.ActorSystem, ID: "router"}, taskID(message.TaskID), eventAgentID(recipient), message.ThreadID, message.CorrelationID, cause, map[string]any{"message_id": message.ID, "recipient": recipient, "state": core.DeliveryFailed, "reason": reason}); err != nil {
		return err
	}
	return nil
}

func (r *Router) AgentAvailable(id core.AgentID) {
	snapshot := r.Snapshot()
	now := r.clock()
	for _, message := range snapshot.Messages {
		if message.RecipientDelivery[id] != core.DeliveryPending {
			continue
		}
		cause := r.messageAcceptedID(message.ID)
		if now.Sub(message.CreatedAt) >= r.limits.MaxPendingAge {
			var failedTask core.TaskID
			reason := "recipient unavailable beyond maximum pending age"
			r.mu.Lock()
			current := r.messages[message.ID]
			if current.RecipientDelivery[id] == core.DeliveryPending {
				failureErr := r.failRecipientLocked(&current, id, reason, cause)
				failedTask = delegationTask(current)
				if failedTask != "" && failureErr == nil && len(r.events) > 0 {
					r.taskEvents[failedTask] = r.events[len(r.events)-1].ID
				}
				r.messages[message.ID] = cloneMessage(current)
			}
			r.mu.Unlock()
			if failedTask != "" {
				_, _ = r.finishTask(context.Background(), failedTask, core.TaskFailed, "failed: delegation delivery failed: "+reason, "delegation delivery failed: "+reason, now)
			}
			continue
		}
		_ = r.tryDeliver(context.Background(), message.ID, id, cause)
	}
}

func (r *Router) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := Snapshot{}
	for _, id := range r.messageOrder {
		out.Messages = append(out.Messages, cloneMessage(r.messages[id]))
	}
	for _, id := range r.taskOrder {
		out.Tasks = append(out.Tasks, cloneTask(r.tasks[id]))
	}
	for _, id := range r.consultOrder {
		out.Consultations = append(out.Consultations, cloneConsultation(r.consultations[id]))
	}
	for _, event := range r.events {
		out.Events = append(out.Events, cloneEvent(event))
	}
	return out
}

func (r *Router) message(id core.MessageID) core.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneMessage(r.messages[id])
}

func (r *Router) messageAcceptedID(id core.MessageID) core.EventID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.messageAccepted[id]
}

func (r *Router) reject(sender core.AgentID, thread core.ThreadID, task *core.TaskID, reason string, details map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.emitLocked("message.rejected", actorFor(sender), taskIDIfValid(task), "", threadIDIfValid(thread), "", "", map[string]any{"reason": reason, "details": details}); err != nil {
		return fmt.Errorf("%s (record rejection: %w)", reason, err)
	}
	return errors.New(reason)
}

func (r *Router) emitLocked(eventType string, actor core.Actor, task core.TaskID, agent core.AgentID, thread core.ThreadID, correlation, causation core.EventID, payload any) (core.Event, error) {
	return r.emitAtLocked(r.clock().UTC(), eventType, actor, task, agent, thread, correlation, causation, payload)
}

func (r *Router) emitAtLocked(at time.Time, eventType string, actor core.Actor, task core.TaskID, agent core.AgentID, thread core.ThreadID, correlation, causation core.EventID, payload any) (core.Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return core.Event{}, fmt.Errorf("encode %s event: %w", eventType, err)
	}
	id, err := r.ids.New("event")
	if err != nil {
		return core.Event{}, err
	}
	if actor.ID == "" {
		actor = core.Actor{Kind: core.ActorSystem, ID: "router"}
	}
	event := core.Event{
		ID: core.EventID(id), SchemaVersion: core.EventSchemaVersion,
		Time: at.UTC(), Workspace: r.workspaceID, Actor: actor,
		Source: "comms", Type: eventType, Category: core.CategoryOf(eventType),
		TaskID: task, AgentID: agent, ThreadID: thread,
		CorrelationID: correlation, CausationID: causation, Payload: data,
	}
	if event.CorrelationID == "" && task != "" {
		if item, ok := r.tasks[task]; ok {
			event.CorrelationID = item.CorrelationID
		}
	}
	if event.CorrelationID == "" && thread != "" {
		event.CorrelationID = r.threadRoots[thread]
	}
	if event.CorrelationID == "" {
		event.CorrelationID = event.ID
	}
	sequenced, err := r.sequencer.Assign(event)
	if err != nil {
		return core.Event{}, err
	}
	r.events = append(r.events, cloneEvent(sequenced))
	if thread != "" && r.threadRoots[thread] == "" {
		r.threadRoots[thread] = sequenced.CorrelationID
	}
	return sequenced, nil
}

func (r *Router) checkUniqueIDLocked(id core.MessageID) error {
	if _, exists := r.messages[id]; exists {
		return fmt.Errorf("duplicate message ID %q", id)
	}
	return nil
}

func actorFor(id core.AgentID) core.Actor {
	if id == core.HumanAgentID {
		return core.Actor{Kind: core.ActorOperator, ID: string(id)}
	}
	if id.Validate() == nil {
		return core.Actor{Kind: core.ActorAgent, ID: string(id)}
	}
	return core.Actor{Kind: core.ActorSystem, ID: "router"}
}

func taskID(id *core.TaskID) core.TaskID {
	if id == nil {
		return ""
	}
	return *id
}

func taskIDIfValid(id *core.TaskID) core.TaskID {
	if id == nil || id.Validate() != nil {
		return ""
	}
	return *id
}

func firstRecipient(ids []core.AgentID) core.AgentID {
	if len(ids) == 0 {
		return ""
	}
	return eventAgentID(ids[0])
}

func eventAgentID(id core.AgentID) core.AgentID {
	if id == core.HumanAgentID {
		return ""
	}
	return id
}

func threadIDIfValid(id core.ThreadID) core.ThreadID {
	if id.Validate() != nil {
		return ""
	}
	return id
}

func aggregateDelivery(states map[core.AgentID]core.DeliveryStatus) core.DeliveryStatus {
	if len(states) == 0 {
		return core.DeliveryPending
	}
	allDelivered := true
	for _, state := range states {
		if state == core.DeliveryPending {
			return core.DeliveryPending
		}
		if state != core.DeliveryDelivered && state != core.DeliveryRead {
			allDelivered = false
		}
	}
	if allDelivered {
		return core.DeliveryDelivered
	}
	return core.DeliveryFailed
}

func deliveryKey(message core.MessageID, recipient core.AgentID) string {
	return string(message) + "/" + string(recipient)
}

func delegationTask(message core.Message) core.TaskID {
	if message.Type == core.MessageTaskDelegation {
		return taskID(message.TaskID)
	}
	return ""
}

func containsAgent(ids []core.AgentID, id core.AgentID) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

func cloneMessage(message core.Message) core.Message {
	message.Recipients = append([]core.AgentID(nil), message.Recipients...)
	message.RecipientDelivery = cloneMap(message.RecipientDelivery)
	message.DeliveryReason = cloneMap(message.DeliveryReason)
	if message.TaskID != nil {
		id := *message.TaskID
		message.TaskID = &id
	}
	if message.ReplyTo != nil {
		id := *message.ReplyTo
		message.ReplyTo = &id
	}
	return message
}

func cloneTask(task core.Task) core.Task {
	task.Participants = append([]core.AgentID(nil), task.Participants...)
	task.DependsOn = append([]core.TaskID(nil), task.DependsOn...)
	task.EventRefs = append([]core.EventID(nil), task.EventRefs...)
	task.ContextRefs = append([]string(nil), task.ContextRefs...)
	if task.Parent != nil {
		id := *task.Parent
		task.Parent = &id
	}
	if task.PermissionPolicy != nil {
		policy := *task.PermissionPolicy
		policy.AllowTools = append([]string(nil), policy.AllowTools...)
		policy.DenyTools = append([]string(nil), policy.DenyTools...)
		task.PermissionPolicy = &policy
	}
	if task.Limits.Budget.Deadline != nil {
		deadline := *task.Limits.Budget.Deadline
		task.Limits.Budget.Deadline = &deadline
	}
	if task.StartedAt != nil {
		started := *task.StartedAt
		task.StartedAt = &started
	}
	if task.FinishedAt != nil {
		finished := *task.FinishedAt
		task.FinishedAt = &finished
	}
	return task
}

func cloneConsultation(consultation Consultation) Consultation {
	consultation.Evidence = append([]string(nil), consultation.Evidence...)
	consultation.References = append([]string(nil), consultation.References...)
	if consultation.Result != nil {
		result := *consultation.Result
		result.Evidence = append([]string(nil), result.Evidence...)
		result.References = append([]string(nil), result.References...)
		consultation.Result = &result
	}
	return consultation
}

func cloneEvent(event core.Event) core.Event {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return event
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}
	copy := make(map[K]V, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func normaliseObjective(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func pairKey(requester, target core.AgentID) string {
	return string(requester) + "->" + string(target)
}

func sortedAgentIDs(ids []core.AgentID) []core.AgentID {
	out := append([]core.AgentID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
