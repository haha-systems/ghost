package comms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

// Consult opens a bounded opinion request. The consultant does not own a task
// and their findings are returned as a separate, non-authoritative result.
func (r *Router) Consult(ctx context.Context, request ConsultationRequest) (Consultation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(request.Question) == "" {
		return Consultation{}, r.reject(request.Requester, "", taskIDPtr(request.ParentTaskID), "consultation question is required", nil)
	}
	if request.Timeout < 0 {
		return Consultation{}, r.reject(request.Requester, "", taskIDPtr(request.ParentTaskID), "consultation timeout cannot be negative", nil)
	}
	if request.RequiresApproval {
		return Consultation{}, r.reject(request.Requester, "", taskIDPtr(request.ParentTaskID), "consultation requires human approval", nil)
	}
	if request.Requester != core.HumanAgentID {
		if _, ok := r.directory.Agent(request.Requester); !ok {
			return Consultation{}, r.reject(request.Requester, "", taskIDPtr(request.ParentTaskID), "unknown requester", nil)
		}
	}
	target, ok := r.directory.Agent(request.Target)
	if !ok || request.Target.Validate() != nil {
		return Consultation{}, r.reject(request.Requester, "", taskIDPtr(request.ParentTaskID), "unknown consultation target", map[string]any{"target": request.Target})
	}
	if !r.directory.CanMessage(request.Requester, request.Target) {
		return Consultation{}, r.reject(request.Requester, "", taskIDPtr(request.ParentTaskID), "consultation permission denied", map[string]any{"target": request.Target})
	}
	requesterPolicy := core.PermissionPolicy{Mode: core.PermissionModeInherit}
	if request.Requester != core.HumanAgentID {
		sender, _ := r.directory.Agent(request.Requester)
		requesterPolicy = sender.PermissionPolicy
	}
	requestedPolicy := core.PermissionPolicy{Mode: core.PermissionModeInherit}
	if request.PermissionPolicy != nil {
		requestedPolicy = *request.PermissionPolicy
	}
	if _, approval, err := intersectPermissions(requesterPolicy, target.PermissionPolicy, requestedPolicy); err != nil {
		return Consultation{}, r.reject(request.Requester, "", taskIDPtr(request.ParentTaskID), err.Error(), nil)
	} else if approval {
		return Consultation{}, r.reject(request.Requester, "", taskIDPtr(request.ParentTaskID), "consultation permission requires human approval", nil)
	}

	now := r.clock().UTC()
	deadline := now.Add(r.limits.DefaultTimeout)
	if request.Timeout > 0 && request.Timeout < r.limits.DefaultTimeout {
		deadline = now.Add(request.Timeout)
	}
	thread := core.ThreadID("")
	var parentTask *core.Task
	if request.ParentTaskID != "" {
		r.mu.Lock()
		parent, found := r.tasks[request.ParentTaskID]
		r.mu.Unlock()
		if !found {
			return Consultation{}, r.reject(request.Requester, "", &request.ParentTaskID, "unknown parent task", nil)
		}
		if parent.Status.IsTerminal() {
			return Consultation{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "parent task is terminal", nil)
		}
		if request.Requester != parent.Owner && !(request.Requester == core.HumanAgentID && parent.Requester == core.HumanAgentID) {
			return Consultation{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "requester does not own parent task", nil)
		}
		thread = parent.ThreadID
		parentTask = &parent
	}
	if thread == "" {
		id, err := r.ids.New("thread")
		if err != nil {
			return Consultation{}, err
		}
		thread = core.ThreadID(id)
	}
	consultationID, err := r.ids.New("consultation")
	if err != nil {
		return Consultation{}, err
	}
	consultation := Consultation{
		ID: core.ConsultationID(consultationID), Requester: request.Requester, Target: request.Target,
		ParentTaskID: request.ParentTaskID, Question: strings.TrimSpace(request.Question),
		Evidence: append([]string(nil), request.Evidence...), References: append([]string(nil), request.References...),
		Status: ConsultationOpen, CreatedAt: now, Deadline: deadline, CausationID: request.CausationID,
	}
	if parentTask != nil {
		consultation.CorrelationID = parentTask.CorrelationID
	}
	r.mu.Lock()
	active := 0
	for _, existing := range r.consultations {
		if existing.Target == request.Target && existing.Status == ConsultationOpen {
			active++
		}
	}
	if active >= r.limits.MaxConcurrentConsultationsPerAgent {
		r.mu.Unlock()
		return Consultation{}, r.reject(request.Requester, thread, taskIDPtr(request.ParentTaskID), "consultation concurrency limit reached", map[string]any{"target": request.Target, "limit": r.limits.MaxConcurrentConsultationsPerAgent})
	}
	event, err := r.emitLocked("coordination.consultation_requested", actorFor(request.Requester), request.ParentTaskID, request.Target, thread, consultation.CorrelationID, request.CausationID, map[string]any{"consultation": consultation})
	if err != nil {
		r.mu.Unlock()
		return Consultation{}, err
	}
	if consultation.CorrelationID == "" {
		consultation.CorrelationID = event.CorrelationID
	}
	r.consultations[consultation.ID] = cloneConsultation(consultation)
	r.consultOrder = append(r.consultOrder, consultation.ID)
	r.mu.Unlock()

	requestBody, _ := json.Marshal(map[string]any{
		"consultation_id": consultation.ID, "question": consultation.Question,
		"evidence": consultation.Evidence, "references": consultation.References,
	})
	message := core.Message{
		Sender: request.Requester, Recipients: []core.AgentID{request.Target}, ThreadID: thread,
		Type: core.MessageRequest, Content: string(requestBody), CausationID: event.ID,
		CorrelationID: consultation.CorrelationID,
	}
	if parentTask != nil {
		id := parentTask.ID
		message.TaskID = &id
	}
	accepted, acceptedEvent, err := r.acceptMessageAt(message, false, now)
	if err != nil {
		r.mu.Lock()
		consultation = r.consultations[consultation.ID]
		consultation.Status = ConsultationFailed
		consultation.FailureReason = "request message rejected: " + err.Error()
		_, _ = r.emitAtLocked(now, "coordination.consultation_failed", core.Actor{Kind: core.ActorSystem, ID: "router"}, consultation.ParentTaskID, consultation.Target, thread, consultation.CorrelationID, event.ID, map[string]any{"consultation_id": consultation.ID, "reason": consultation.FailureReason})
		r.consultations[consultation.ID] = cloneConsultation(consultation)
		r.mu.Unlock()
		return Consultation{}, err
	}
	r.mu.Lock()
	consultation = r.consultations[consultation.ID]
	consultation.RequestMessageID = accepted.ID
	consultation.CausationID = acceptedEvent
	r.consultations[consultation.ID] = cloneConsultation(consultation)
	r.mu.Unlock()
	for _, recipient := range accepted.Recipients {
		if err := r.tryDeliver(ctx, accepted.ID, recipient, acceptedEvent); err != nil {
			return consultation, err
		}
	}
	return cloneConsultation(consultation), nil
}

// ResolveConsultation closes a request and returns a structured opinion to
// the original requester. The caller must identify as the named consultant.
func (r *Router) ResolveConsultation(ctx context.Context, id core.ConsultationID, responder core.AgentID, result ConsultationResult) (Consultation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(result.Findings) == "" || strings.TrimSpace(result.Uncertainty) == "" {
		return Consultation{}, errors.New("consultation findings and uncertainty are required")
	}
	now := r.clock().UTC()
	r.mu.Lock()
	consultation, ok := r.consultations[id]
	if !ok {
		r.mu.Unlock()
		return Consultation{}, fmt.Errorf("unknown consultation %q", id)
	}
	if consultation.Target != responder {
		r.mu.Unlock()
		return Consultation{}, errors.New("only the named consultant may resolve this consultation")
	}
	if consultation.Status != ConsultationOpen {
		r.mu.Unlock()
		return Consultation{}, errors.New("consultation is not open")
	}
	result.Question = consultation.Question
	if result.Evidence == nil {
		result.Evidence = append([]string(nil), consultation.Evidence...)
	} else {
		result.Evidence = append([]string(nil), result.Evidence...)
	}
	if result.References == nil {
		result.References = append([]string(nil), consultation.References...)
	} else {
		result.References = append([]string(nil), result.References...)
	}
	consultation.Status = ConsultationCompleted
	consultation.Result = &result
	trigger := consultation.CausationID
	event, err := r.emitLocked("coordination.consultation_completed", actorFor(responder), consultation.ParentTaskID, responder, "", consultation.CorrelationID, trigger, map[string]any{"consultation_id": id, "result": result})
	if err != nil {
		r.mu.Unlock()
		return Consultation{}, err
	}
	consultation.CausationID = event.ID
	r.consultations[id] = cloneConsultation(consultation)
	r.mu.Unlock()

	content, err := json.Marshal(result)
	if err != nil {
		return Consultation{}, err
	}
	replyTo := consultation.RequestMessageID
	message := core.Message{
		Sender: responder, Recipients: []core.AgentID{consultation.Requester},
		ThreadID: r.consultationThread(consultation), Type: core.MessageReply,
		ReplyTo: &replyTo, Content: string(content), CorrelationID: consultation.CorrelationID,
		CausationID: event.ID,
	}
	if _, _, err := r.acceptMessageAt(message, true, now); err != nil {
		return cloneConsultation(consultation), fmt.Errorf("consultation completed but result message was not accepted: %w", err)
	}
	accepted := r.latestMessage(consultation.RequestMessageID, core.MessageReply)
	for _, recipient := range accepted.Recipients {
		if err := r.tryDeliver(ctx, accepted.ID, recipient, r.messageAcceptedID(accepted.ID)); err != nil {
			return cloneConsultation(consultation), err
		}
	}
	return cloneConsultation(consultation), nil
}

// FailConsultation closes an open consultation with an inspectable reason.
func (r *Router) FailConsultation(ctx context.Context, id core.ConsultationID, responder core.AgentID, reason string) (Consultation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(reason) == "" {
		return Consultation{}, errors.New("consultation failure reason is required")
	}
	r.mu.Lock()
	consultation, ok := r.consultations[id]
	if !ok {
		r.mu.Unlock()
		return Consultation{}, fmt.Errorf("unknown consultation %q", id)
	}
	if consultation.Target != responder {
		r.mu.Unlock()
		return Consultation{}, errors.New("only the named consultant may fail this consultation")
	}
	if consultation.Status != ConsultationOpen {
		r.mu.Unlock()
		return Consultation{}, errors.New("consultation is not open")
	}
	consultation.Status = ConsultationFailed
	consultation.FailureReason = reason
	event, err := r.emitLocked("coordination.consultation_failed", actorFor(responder), consultation.ParentTaskID, responder, "", consultation.CorrelationID, consultation.CausationID, map[string]any{"consultation_id": id, "reason": reason})
	if err != nil {
		r.mu.Unlock()
		return Consultation{}, err
	}
	consultation.CausationID = event.ID
	r.consultations[id] = cloneConsultation(consultation)
	r.mu.Unlock()
	_ = ctx // reserved for transport when consultation failure notices are added
	return cloneConsultation(consultation), nil
}

func (r *Router) consultationThread(consultation Consultation) core.ThreadID {
	if consultation.ParentTaskID != "" {
		if task, ok := r.Snapshot().Task(consultation.ParentTaskID); ok {
			return task.ThreadID
		}
	}
	for _, message := range r.Snapshot().Messages {
		if message.ID == consultation.RequestMessageID {
			return message.ThreadID
		}
	}
	return ""
}

func (r *Router) latestMessage(replyTo core.MessageID, kind core.MessageType) core.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.messageOrder) - 1; i >= 0; i-- {
		message := r.messages[r.messageOrder[i]]
		if message.Type == kind && message.ReplyTo != nil && *message.ReplyTo == replyTo {
			return cloneMessage(message)
		}
	}
	return core.Message{}
}

func taskIDPtr(id core.TaskID) *core.TaskID {
	if id == "" {
		return nil
	}
	copy := id
	return &copy
}

func (r *Router) expireConsultationsLocked(now time.Time) {
	for _, id := range r.consultOrder {
		consultation := r.consultations[id]
		if consultation.Status != ConsultationOpen || consultation.Deadline.After(now) {
			continue
		}
		consultation.Status = ConsultationTimedOut
		consultation.FailureReason = "timeout"
		event, err := r.emitAtLocked(now, "coordination.consultation_timed_out", core.Actor{Kind: core.ActorSystem, ID: "router"}, consultation.ParentTaskID, consultation.Target, "", consultation.CorrelationID, consultation.CausationID, map[string]any{"consultation_id": id, "reason": "timeout"})
		if err == nil {
			consultation.CausationID = event.ID
		}
		r.consultations[id] = cloneConsultation(consultation)
	}
}

func (r *Router) cancelConsultationsLocked(tasks []core.TaskID, reason string, now time.Time, causes map[core.TaskID]core.EventID) {
	set := map[core.TaskID]bool{}
	for _, id := range tasks {
		set[id] = true
	}
	for _, id := range r.consultOrder {
		consultation := r.consultations[id]
		if consultation.Status != ConsultationOpen || !set[consultation.ParentTaskID] {
			continue
		}
		consultation.Status = ConsultationCancelled
		consultation.FailureReason = reason
		cause := causes[consultation.ParentTaskID]
		_, _ = r.emitAtLocked(now, "coordination.consultation_cancelled", core.Actor{Kind: core.ActorSystem, ID: "router"}, consultation.ParentTaskID, consultation.Target, "", consultation.CorrelationID, cause, map[string]any{"consultation_id": id, "reason": reason})
		consultation.CausationID = cause
		r.consultations[id] = cloneConsultation(consultation)
	}
}
