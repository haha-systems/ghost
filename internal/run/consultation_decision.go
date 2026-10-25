package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
)

type consultationCandidate struct {
	ID   core.AgentID `json:"id"`
	Name string       `json:"name"`
}

type pendingConsultDecision struct {
	request      core.DecisionRequest
	failedTaskID core.TaskID
	parentTaskID core.TaskID
	parentAgent  core.AgentID
	cancel       context.CancelFunc
}

func (o *owner) requestAutoConsultForFailures(snapshot comms.Snapshot) {
	if !o.autoConsult || o.decisions == nil || o.workspace == nil {
		return
	}
	roster := o.workspace.snapshot().Agents
	capabilitiesAbsent := true
	for _, agent := range roster {
		if len(agent.Capabilities) > 0 {
			capabilitiesAbsent = false
			break
		}
	}
	for _, failed := range snapshot.Tasks {
		if failed.Status != core.TaskFailed || failed.Parent == nil || o.autoConsultedTasks[failed.ID] {
			continue
		}
		parent, ok := snapshot.Task(*failed.Parent)
		if !ok {
			o.autoConsultedTasks[failed.ID] = true
			continue
		}
		candidates := make([]consultationCandidate, 0, len(roster))
		actions := []string{"none"}
		for _, agent := range roster {
			if agent.ID == failed.Owner {
				continue
			}
			if !capabilitiesAbsent && !hasCapability(agent.Capabilities, core.CapabilityConsultation) {
				continue
			}
			candidates = append(candidates, consultationCandidate{ID: agent.ID, Name: agent.DisplayName})
			actions = append(actions, "consult:"+agent.DisplayName)
		}
		o.autoConsultedTasks[failed.ID] = true
		requestID, err := o.ids.New("event")
		if err != nil {
			o.coreProjectionError(err)
			continue
		}
		state, err := json.Marshal(struct {
			FailedTaskID  core.TaskID             `json:"failed_task_id"`
			Goal          string                  `json:"goal"`
			FailureReason string                  `json:"failure_reason"`
			ParentAgent   core.AgentID            `json:"parent_agent"`
			Candidates    []consultationCandidate `json:"candidate_agents"`
		}{
			FailedTaskID: failed.ID, Goal: compactDecisionText(failed.Objective),
			FailureReason: compactDecisionText(failed.FailureReason), ParentAgent: parent.Owner,
			Candidates: candidates,
		})
		if err != nil {
			o.coreProjectionError(err)
			continue
		}
		request := core.DecisionRequest{
			ID: core.EventID(requestID), Kind: core.DecisionKindConsultation, TaskID: parent.ID,
			RequestingAgent: parent.Owner, State: state, Actions: actions,
			Constraints: core.DecisionConstraints{Notes: "failed delegated child; one named consultant; no broadcast"},
			Budget:      core.DecisionBudget{Timeout: o.config.Decisions.Timeout()},
		}
		ctx, cancel := context.WithCancel(o.ctx)
		pending := pendingConsultDecision{request: request, failedTaskID: failed.ID, parentTaskID: parent.ID, parentAgent: parent.Owner, cancel: cancel}
		o.pendingConsults[request.ID] = pending
		o.consultPendingTask[parent.ID] = request.ID
		o.requestDecisionWithContext(request, ctx)
	}
}

func hasCapability(capabilities []core.Capability, wanted core.Capability) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func compactDecisionText(value string) string {
	value = strings.TrimSpace(value)
	const limit = 500
	if len(value) > limit {
		return value[:limit] + "…"
	}
	return value
}

func (o *owner) applyConsultationDecision(requestID core.EventID, outcome decision.Outcome) {
	pending, ok := o.pendingConsults[requestID]
	if !ok {
		return
	}
	if !o.autoConsult {
		o.finishConsultDecision(pending, "", fmt.Errorf("auto consultation disabled"))
		return
	}
	if outcome.Status != decision.StatusRecommended || outcome.Response == nil {
		reason := outcome.Reason
		if reason == "" {
			reason = string(outcome.Status)
		}
		o.finishConsultDecision(pending, "", fmt.Errorf("decision unavailable: %s", reason))
		return
	}
	action := outcome.Response.Action
	if action == "none" {
		o.recordDecisionIgnored(requestID, action, "no consultation selected")
		o.clearPendingConsult(pending)
		return
	}
	targetName, ok := strings.CutPrefix(action, "consult:")
	if !ok || strings.TrimSpace(targetName) == "" {
		o.finishConsultDecision(pending, action, fmt.Errorf("invalid consultation action"))
		return
	}
	target, ok := o.workspace.agentID(targetName)
	if !ok || target == pending.failedTaskOwner(o.workspace) {
		o.finishConsultDecision(pending, action, fmt.Errorf("consultation target is unavailable"))
		return
	}
	parent, ok := o.workspace.task(pending.parentTaskID)
	if !ok {
		o.finishConsultDecision(pending, action, fmt.Errorf("parent task is unavailable"))
		return
	}
	question := fmt.Sprintf("Review this failed delegated task and suggest a next step.\nGoal: %s\nFailure: %s", compactDecisionText(pending.failedGoal(o.workspace)), compactDecisionText(pending.failedReason(o.workspace)))
	var consultation comms.Consultation
	done := o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
		var err error
		consultation, err = router.Consult(o.ctx, comms.ConsultationRequest{
			Requester: pending.parentAgent, Target: target, ParentTaskID: parent.ID,
			Question: question, Evidence: []string{string(pending.failedTaskID)}, CausationID: requestID,
		})
		return err
	}})
	o.workers.Add(1)
	go func() {
		defer o.workers.Done()
		err := <-done
		if err == nil && consultation.ID == "" {
			err = fmt.Errorf("router did not create a consultation")
		}
		_ = o.controller.call(func(current *owner) {
			current.finishConsultDecision(pending, action, err)
			current.publishBoundary()
		})
	}()
}

func (pending pendingConsultDecision) failedTaskOwner(workspace *workspaceProjection) core.AgentID {
	task, _ := workspace.task(pending.failedTaskID)
	return task.Owner
}

func (pending pendingConsultDecision) failedGoal(workspace *workspaceProjection) string {
	task, _ := workspace.task(pending.failedTaskID)
	return task.Objective
}

func (pending pendingConsultDecision) failedReason(workspace *workspaceProjection) string {
	task, _ := workspace.task(pending.failedTaskID)
	return task.FailureReason
}

func (o *owner) finishConsultDecision(pending pendingConsultDecision, action string, err error) {
	if err != nil {
		o.recordDecisionIgnored(pending.request.ID, action, err.Error())
	} else {
		o.recordDecisionApplied(pending.request.ID, action)
	}
	o.clearPendingConsult(pending)
}

func (o *owner) clearPendingConsult(pending pendingConsultDecision) {
	pending.cancel()
	delete(o.pendingConsults, pending.request.ID)
	if o.consultPendingTask[pending.parentTaskID] == pending.request.ID {
		delete(o.consultPendingTask, pending.parentTaskID)
	}
	o.maybeCompleteParent(pending.parentTaskID)
}
