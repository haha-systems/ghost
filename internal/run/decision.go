package run

import (
	"fmt"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
)

type decisionResultMsg struct {
	request core.DecisionRequest
	outcome decision.Outcome
}

// requestDecision records the input, then runs the agent outside the owner loop.
func (o *owner) requestDecision(request core.DecisionRequest) {
	if o.stopping || o.decisions == nil {
		return
	}
	request.State = append([]byte(nil), request.State...)
	request.Actions = append([]string(nil), request.Actions...)
	request.MemoryEvidence = append([]string(nil), request.MemoryEvidence...)
	if request.ID == "" {
		id, err := o.ids.New("event")
		if err != nil {
			o.coreProjectionError(err)
			return
		}
		request.ID = core.EventID(id)
	}
	if request.TaskID == "" {
		request.TaskID = o.activeTaskID
	}
	o.publishDecisionEvent("decision.requested", request.ID, request, request.TaskID, request.RequestingAgent)
	o.workers.Add(1)
	go func() {
		defer o.workers.Done()
		outcome := o.decisions.Decide(o.ctx, request)
		_ = o.controller.call(func(owner *owner) {
			owner.handleDecisionResult(decisionResultMsg{request: request, outcome: outcome})
			owner.publishBoundary()
		})
	}()
}

func (o *owner) handleDecisionResult(message decisionResultMsg) {
	if o.stopping {
		return
	}
	switch message.outcome.Status {
	case decision.StatusRecommended:
		if message.outcome.Response == nil {
			o.publishDecisionEvent("decision.unavailable", message.request.ID, message.outcome, message.request.TaskID, message.request.RequestingAgent)
			return
		}
		o.publishDecisionEvent("decision.recommended", message.request.ID, message.outcome.Response, message.request.TaskID, message.request.RequestingAgent)
	case decision.StatusBudgetExhausted:
		o.publishDecisionEvent("decision.budget_exhausted", message.request.ID, message.outcome, message.request.TaskID, message.request.RequestingAgent)
	default:
		o.publishDecisionEvent("decision.unavailable", message.request.ID, message.outcome, message.request.TaskID, message.request.RequestingAgent)
	}
}

func (o *owner) publishDecisionEvent(eventType string, requestID core.EventID, payload any, taskID core.TaskID, agentID core.AgentID) {
	ctx := o.eventContext()
	ctx.TaskID = ""
	ctx.ThreadID = ""
	if taskID.Validate() == nil {
		ctx.TaskID = taskID
		if task, ok := o.workspace.task(taskID); ok {
			ctx.ThreadID = task.ThreadID
		}
	}
	ctx.CorrelationID = requestID
	if agentID.Validate() != nil {
		agentID = ""
	}
	item, err := newCoreEvent(ctx, timeNow(), core.Actor{Kind: core.ActorSystem, ID: "decision"}, "DECISION", eventType, agentID, "", payload)
	if err != nil {
		o.coreProjectionError(fmt.Errorf("create %s event: %w", eventType, err))
		return
	}
	item.CorrelationID = requestID
	item.CausationID = requestID
	o.publishCoreEvent(item)
}

type decisionExecutionRecord struct {
	RequestID core.EventID `json:"request_id"`
	Action    string       `json:"action"`
	Reason    string       `json:"reason,omitempty"`
}

func (o *owner) recordDecisionApplied(requestID core.EventID, action string) {
	o.publishDecisionEvent("decision.applied", requestID, decisionExecutionRecord{RequestID: requestID, Action: action}, o.activeTaskID, "")
}

func (o *owner) recordDecisionOverridden(requestID core.EventID, action, reason string) {
	o.publishDecisionEvent("decision.overridden", requestID, decisionExecutionRecord{RequestID: requestID, Action: action, Reason: reason}, o.activeTaskID, "")
}

func (o *owner) recordDecisionIgnored(requestID core.EventID, action, reason string) {
	o.publishDecisionEvent("decision.ignored", requestID, decisionExecutionRecord{RequestID: requestID, Action: action, Reason: reason}, o.activeTaskID, "")
}
