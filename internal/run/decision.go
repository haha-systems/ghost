package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
	"github.com/haha-systems/ghost/internal/runtime"
)

type pendingCESAuto struct {
	request    core.DecisionRequest
	goal       string
	recipients []core.AgentID
	broadcast  bool
	global     bool
	cancel     context.CancelFunc
	superseded bool
	cancelled  bool
}

type decisionResultMsg struct {
	request core.DecisionRequest
	outcome decision.Outcome
}

// requestDecision records the input, then runs the agent outside the owner loop.
func (o *owner) requestDecision(request core.DecisionRequest) core.EventID {
	return o.requestDecisionWithContext(request, o.ctx)
}

func (o *owner) requestDecisionWithContext(request core.DecisionRequest, ctx context.Context) core.EventID {
	if o.stopping || o.decisions == nil {
		return ""
	}
	request.State = append([]byte(nil), request.State...)
	request.Actions = append([]string(nil), request.Actions...)
	request.MemoryEvidence = append([]string(nil), request.MemoryEvidence...)
	if request.ID == "" {
		id, err := o.ids.New("event")
		if err != nil {
			o.coreProjectionError(err)
			return ""
		}
		request.ID = core.EventID(id)
	}
	if request.TaskID == "" {
		request.TaskID = o.activeTaskID
	}
	o.publishDecisionEvent("decision.requested", request.ID, request, request.TaskID, request.RequestingAgent)
	if ctx == nil {
		ctx = o.ctx
	}
	o.workers.Add(1)
	go func() {
		defer o.workers.Done()
		outcome := o.decisions.Decide(ctx, request)
		_ = o.controller.call(func(owner *owner) {
			owner.handleDecisionResult(decisionResultMsg{request: request, outcome: outcome})
			owner.publishBoundary()
		})
	}()
	return request.ID
}

func (o *owner) handleDecisionResult(message decisionResultMsg) {
	if o.stopping {
		return
	}
	pending := o.autoDecisions[message.request.ID]
	isAuto := pending != nil
	switch message.outcome.Status {
	case decision.StatusRecommended:
		if message.outcome.Response == nil {
			o.publishDecisionEvent("decision.unavailable", message.request.ID, message.outcome, message.request.TaskID, message.request.RequestingAgent)
		} else {
			o.publishDecisionEvent("decision.recommended", message.request.ID, message.outcome.Response, message.request.TaskID, message.request.RequestingAgent)
		}
	case decision.StatusBudgetExhausted:
		o.publishDecisionEvent("decision.budget_exhausted", message.request.ID, message.outcome, message.request.TaskID, message.request.RequestingAgent)
	default:
		o.publishDecisionEvent("decision.unavailable", message.request.ID, message.outcome, message.request.TaskID, message.request.RequestingAgent)
	}
	if !isAuto {
		return
	}
	if pending.superseded {
		o.recordDecisionIgnored(message.request.ID, responseAction(message.outcome), "superseded by operator override")
		pending.cancel()
		delete(o.autoDecisions, message.request.ID)
		if o.pendingCESAuto == pending {
			o.pendingCESAuto = nil
		}
		return
	}
	if pending.cancelled {
		pending.cancel()
		delete(o.autoDecisions, message.request.ID)
		if o.pendingCESAuto == pending {
			o.pendingCESAuto = nil
		}
		return
	}
	o.applyCESAutoOutcome(pending, message.outcome)
}

func (o *owner) publishDecisionEvent(eventType string, requestID core.EventID, payload any, taskID core.TaskID, agentID core.AgentID) {
	actor := core.Actor{Kind: core.ActorSystem, ID: "decision"}
	if eventType == "decision.overridden" {
		actor = core.Actor{Kind: core.ActorOperator, ID: string(core.HumanAgentID)}
	}
	o.publishDecisionEventAs(eventType, requestID, payload, taskID, agentID, actor)
}

func (o *owner) publishDecisionEventAs(eventType string, requestID core.EventID, payload any, taskID core.TaskID, agentID core.AgentID, actor core.Actor) {
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
	item, err := newCoreEvent(ctx, timeNow(), actor, "DECISION", eventType, agentID, "", payload)
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
	Source    string       `json:"source,omitempty"`
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

func (o *owner) recordDecisionAppliedFrom(requestID core.EventID, action, source, reason string) {
	o.publishDecisionEvent("decision.applied", requestID, decisionExecutionRecord{RequestID: requestID, Action: action, Source: source, Reason: reason}, o.activeTaskID, "")
}

func decisionAction(policy CESPolicy) string {
	if policy == CESPolicyOn {
		return "ces_on"
	}
	return "ces_off"
}

func policyForDecisionAction(action string) CESPolicy {
	if action == "ces_on" {
		return CESPolicyOn
	}
	return CESPolicyOff
}

func responseAction(outcome decision.Outcome) string {
	if outcome.Response == nil {
		return ""
	}
	return outcome.Response.Action
}

func containsDecisionAction(actions []string, action string) bool {
	for _, offered := range actions {
		if offered == action {
			return true
		}
	}
	return false
}

func (o *owner) beginCESAuto(goal string, global bool) (<-chan error, error) {
	if o.cognition == nil {
		const reason = "unsupported: QAC not configured"
		o.cesAuto = CESAutoStatus{Executed: "ces_off", Source: "fallback", Reason: reason}
		if global {
			o.cesEffective, o.cesPolicyReason = CESPolicyOff, reason
		}
		return o.startStandardWork(goal)
	}
	if o.workspace == nil {
		return nil, errors.New("workspace projection is not initialized")
	}
	requestID, err := o.ids.New("event")
	if err != nil {
		return nil, err
	}
	budgetTaskID, err := o.ids.New("task")
	if err != nil {
		return nil, err
	}
	participants := o.autoStartParticipants()
	ownerResource := o.config.QAC.EntryResource
	if ownerResource == "" && len(participants) > 0 {
		ownerResource = participants[0]
	}
	requestingAgent := core.AgentID("")
	if resource, ok := o.config.QAC.Resources[ownerResource]; ok {
		requestingAgent, _ = o.workspace.agentID(resource.Agent)
	}
	if requestingAgent == "" && len(participants) > 0 {
		requestingAgent, _ = o.workspace.agentID(participants[0])
	}
	if requestingAgent.Validate() != nil {
		return nil, errors.New("CES auto decision has no valid requesting agent")
	}
	state, err := json.Marshal(struct {
		Goal             string   `json:"goal"`
		Participants     []string `json:"participants,omitempty"`
		OwnerResource    string   `json:"owner_resource,omitempty"`
		QACConfigured    bool     `json:"qac_configured"`
		PreviousRunState string   `json:"previous_run_outcome,omitempty"`
	}{Goal: goal, Participants: participants, OwnerResource: ownerResource, QACConfigured: o.cognition != nil, PreviousRunState: o.previousRunOutcome()})
	if err != nil {
		return nil, err
	}
	request := core.DecisionRequest{
		ID: core.EventID(requestID), Kind: core.DecisionKindCESMode,
		TaskID: core.TaskID(budgetTaskID), RequestingAgent: requestingAgent,
		State: state, Actions: []string{"ces_on", "ces_off"},
		Constraints: core.DecisionConstraints{ManualOverride: "operator choice takes precedence", Notes: "CES auto start; delegated children use the standard path"},
	}
	fallback := o.autoFallback()
	ctx, cancel := context.WithCancel(o.ctx)
	pending := &pendingCESAuto{request: request, goal: goal, recipients: append([]core.AgentID(nil), o.startRecipients...), broadcast: o.startBroadcast, global: global, cancel: cancel}
	o.pendingCESAuto = pending
	if o.autoDecisions == nil {
		o.autoDecisions = map[core.EventID]*pendingCESAuto{}
	}
	o.autoDecisions[request.ID] = pending
	o.cesAuto = CESAutoStatus{Pending: true, RequestID: request.ID, Source: "decision"}
	if o.decisions == nil {
		o.publishDecisionEvent("decision.requested", request.ID, request, request.TaskID, request.RequestingAgent)
		outcome := decision.Outcome{Status: decision.StatusUnavailable, Fallback: fallback, Reason: "decision service is not configured"}
		o.publishDecisionEvent("decision.unavailable", request.ID, outcome, request.TaskID, request.RequestingAgent)
		return o.applyCESAutoOutcome(pending, outcome)
	}
	if o.requestDecisionWithContext(request, ctx) == "" {
		cancel()
		delete(o.autoDecisions, request.ID)
		o.pendingCESAuto = nil
		o.cesAuto.Pending = false
		return nil, errors.New("could not request CES decision")
	}
	return nil, nil
}

func (o *owner) autoFallback() string {
	fallback := strings.TrimSpace(o.config.Decisions.Fallback)
	if fallback != "on" && fallback != "off" {
		fallback = "off"
	}
	return fallback
}

func (o *owner) autoStartParticipants() []string {
	if o.workspace == nil {
		return nil
	}
	if o.startBroadcast {
		return o.workspace.agentNamesSnapshot()
	}
	if len(o.startRecipients) > 0 {
		names := make([]string, 0, len(o.startRecipients))
		for _, id := range o.startRecipients {
			if name, ok := o.workspace.agentName(id); ok {
				names = append(names, name)
			}
		}
		return names
	}
	names := make([]string, 0)
	for _, name := range o.workspace.agentNamesSnapshot() {
		if session := o.sessions[name]; session != nil && session.State() == runtime.StateIdle {
			names = append(names, name)
		}
	}
	return names
}

func (o *owner) previousRunOutcome() string {
	if o.workspace == nil {
		return ""
	}
	if o.cesStore != nil {
		state := o.cesStore.State().Task
		if state.Status != "active" && state.Status != "" {
			if state.TerminalReason != "" {
				return string(state.Status) + ":" + state.TerminalReason
			}
			return string(state.Status)
		}
	}
	if task, ok := o.workspace.task(o.activeTaskID); ok && task.Status.IsTerminal() {
		return string(task.Status)
	}
	return ""
}

func (o *owner) applyCESAutoOutcome(pending *pendingCESAuto, outcome decision.Outcome) (<-chan error, error) {
	action, source, reason := "", "decision", ""
	if outcome.Status == decision.StatusRecommended && outcome.Response != nil {
		action = outcome.Response.Action
		if o.pendingCESAuto == pending {
			o.cesAuto.Recommended = action
		}
	} else {
		action = outcome.Fallback
		if action == "" {
			action = o.autoFallback()
		}
		if action != "on" && action != "off" {
			action = "off"
		}
		if action == "on" {
			action = "ces_on"
		} else {
			action = "ces_off"
		}
		source = "fallback"
		reason = outcome.Reason
		if reason == "" {
			reason = string(outcome.Status)
		}
	}
	if o.cognition == nil {
		o.recordDecisionIgnored(pending.request.ID, action, "provider/QAC incompatible")
		action, source, reason = "ces_off", "fallback", "provider/QAC incompatible"
	}
	return o.executeCESAuto(pending, action, source, reason, true)
}

func (o *owner) overridePendingCESAuto(action, source, reason string) error {
	pending := o.pendingCESAuto
	if pending == nil || !o.cesAuto.Pending {
		return errors.New("no CES auto decision is pending")
	}
	if action != "ces_on" && action != "ces_off" {
		return fmt.Errorf("unknown CES action %q", action)
	}
	pending.superseded = true
	o.cesAuto.Pending = false
	o.cesAuto.Source = source
	o.recordDecisionOverridden(pending.request.ID, action, reason)
	if o.cognition == nil {
		o.recordDecisionIgnored(pending.request.ID, action, "provider/QAC incompatible")
		action, source, reason = "ces_off", "fallback", "provider/QAC incompatible"
	}
	_, err := o.executeCESAuto(pending, action, source, reason, false)
	return err
}

func (o *owner) executeCESAuto(pending *pendingCESAuto, action, source, reason string, applied bool) (<-chan error, error) {
	policy := policyForDecisionAction(action)
	if pending.global {
		o.cesEffective = policy
		if source == "fallback" && strings.HasPrefix(reason, "unsupported:") {
			o.cesPolicyReason = reason
		} else {
			o.cesPolicyReason = ""
		}
		if source != "manual" {
			o.emitCESPolicyChanged()
		}
	}
	o.cesAuto.Pending = false
	o.cesAuto.Executed = action
	o.cesAuto.Source = source
	o.cesAuto.Reason = reason
	oldRecipients, oldBroadcast := o.startRecipients, o.startBroadcast
	o.startRecipients = append([]core.AgentID(nil), pending.recipients...)
	o.startBroadcast = pending.broadcast
	var brokerDone <-chan error
	var err error
	if action == "ces_on" {
		o.startCESWork(pending.goal)
	} else {
		brokerDone, err = o.startStandardWork(pending.goal)
	}
	o.startRecipients, o.startBroadcast = oldRecipients, oldBroadcast
	if applied {
		pending.cancel()
		delete(o.autoDecisions, pending.request.ID)
		if o.pendingCESAuto == pending {
			o.pendingCESAuto = nil
		}
	}
	if err != nil {
		o.coreProjectionError(err)
	}
	if applied {
		o.recordDecisionAppliedFrom(pending.request.ID, action, source, reason)
	}
	return brokerDone, err
}

// OverrideDecision applies a human action to the currently pending CES mode
// request. The later agent result remains advisory and is ignored.
func (c *Controller) OverrideDecision(requestID core.EventID, action, reason string) error {
	if err := requestID.Validate(); err != nil {
		return fmt.Errorf("decision request ID: %w", err)
	}
	var result error
	callErr := c.call(func(o *owner) {
		pending := o.pendingCESAuto
		if pending == nil || !o.cesAuto.Pending || pending.request.ID != requestID {
			result = errors.New("CES auto decision is not pending")
			return
		}
		if !containsDecisionAction(pending.request.Actions, action) {
			result = fmt.Errorf("action %q was not offered", action)
			return
		}
		result = o.overridePendingCESAuto(action, "override", reason)
		o.publishBoundary()
	})
	return errors.Join(result, callErr)
}

func (o *owner) recordInterruptedAutoDecisions(events []core.Event) {
	requests := map[core.EventID]bool{}
	completed := map[core.EventID]bool{}
	for _, item := range events {
		switch item.Type {
		case "decision.requested":
			var request core.DecisionRequest
			if json.Unmarshal(item.Payload, &request) == nil && request.Kind == core.DecisionKindCESMode && request.Constraints.Notes == "CES auto start; delegated children use the standard path" {
				requests[request.ID] = true
			}
		case "decision.applied", "decision.overridden", "decision.ignored":
			if item.CorrelationID != "" {
				completed[item.CorrelationID] = true
			}
		}
	}
	ids := make([]string, 0, len(requests))
	for id := range requests {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, rawID := range ids {
		id := core.EventID(rawID)
		if !completed[id] {
			o.recordDecisionIgnored(id, "", "interrupted")
		}
	}
}
