package cognition

import (
	"errors"
	"fmt"
	"strings"

	"github.com/haha-systems/ghost/internal/epistemic"
)

// Orchestrator owns phase routing and work completion. QAC is used only to
// select a resource after this package chooses the phase.
type Orchestrator struct{ store *epistemic.Store }

func NewOrchestrator(store *epistemic.Store) *Orchestrator { return &Orchestrator{store: store} }

func (o *Orchestrator) Current() epistemic.Phase {
	if o == nil || o.store == nil {
		return ""
	}
	return o.store.State().Task.Phase
}

func (o *Orchestrator) Advance() (epistemic.Phase, error) {
	if o == nil || o.store == nil {
		return "", errors.New("orchestrator has no CES store")
	}
	state := o.store.State()
	if state.Task.Status != epistemic.WorkActive {
		return state.Task.Phase, errors.New("work is terminal")
	}
	if targetID, targetKind, reason, ok := nextContradiction(state); ok {
		route := routeFor(targetKind)
		if phaseOrder(route) <= phaseOrder(state.Task.Phase) {
			if route == state.Task.Phase {
				return route, nil
			}
			_, err := o.store.TransitionPhase(epistemic.TransitionRequest{To: route, Reason: reason, TargetID: targetID, TargetKind: targetKind})
			return o.store.State().Task.Phase, err
		}
	}
	switch state.Task.Phase {
	case epistemic.PhaseTriage:
		return o.forward(epistemic.PhaseAbduce, "triage artifact committed")
	case epistemic.PhaseAbduce:
		return o.forward(epistemic.PhaseFrame, "abduction artifact committed")
	case epistemic.PhaseFrame:
		return o.forward(epistemic.PhaseExecute, "frame artifact committed")
	case epistemic.PhaseExecute:
		return o.forward(epistemic.PhaseClose, "execution artifact committed")
	case epistemic.PhaseClose:
		if state.Task.LeadingHypothesis != "" {
			for _, hypothesis := range state.Hypotheses {
				if hypothesis.ID == state.Task.LeadingHypothesis && hypothesis.Status == epistemic.HypothesisConfirmed {
					_, err := o.store.SetTerminal(epistemic.WorkComplete, "")
					return state.Task.Phase, err
				}
			}
		}
		_, err := o.store.SetTerminal(epistemic.WorkIncomplete, "underdetermined")
		return state.Task.Phase, err
	default:
		return state.Task.Phase, fmt.Errorf("unknown phase %q", state.Task.Phase)
	}
}

func (o *Orchestrator) forward(next epistemic.Phase, reason string) (epistemic.Phase, error) {
	_, err := o.store.TransitionPhase(epistemic.TransitionRequest{To: next, Reason: reason})
	return o.store.State().Task.Phase, err
}

func (o *Orchestrator) ResourceUnavailable() error {
	if o == nil || o.store == nil {
		return errors.New("orchestrator has no CES store")
	}
	_, err := o.store.SetTerminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable")
	return err
}

func (o *Orchestrator) ExecutionFailed() error {
	if o == nil || o.store == nil {
		return errors.New("orchestrator has no CES store")
	}
	_, err := o.store.SetTerminal(epistemic.WorkIncomplete, "phase_execution_failed")
	return err
}

func routeFor(kind epistemic.ObjectKind) epistemic.Phase {
	switch kind {
	case epistemic.ObjectHypothesis:
		return epistemic.PhaseAbduce
	case epistemic.ObjectFrame:
		return epistemic.PhaseFrame
	case epistemic.ObjectAction, epistemic.ObjectOutcome:
		return epistemic.PhaseExecute
	default:
		return ""
	}
}

func phaseOrder(phase epistemic.Phase) int {
	switch phase {
	case epistemic.PhaseTriage:
		return 0
	case epistemic.PhaseAbduce:
		return 1
	case epistemic.PhaseFrame:
		return 2
	case epistemic.PhaseExecute:
		return 3
	case epistemic.PhaseClose:
		return 4
	default:
		return -1
	}
}

func nextContradiction(state epistemic.State) (epistemic.ID, epistemic.ObjectKind, string, bool) {
	for i := len(state.Relations) - 1; i >= 0; i-- {
		relation := state.Relations[i]
		if relation.Status != epistemic.RelationActive || relation.Kind != epistemic.RelationContradicts {
			continue
		}
		kind, relevant := epistemic.ObjectKind(""), false
		for _, hypothesis := range state.Hypotheses {
			if hypothesis.ID == relation.TargetID && hypothesis.ID == state.Task.LeadingHypothesis && hypothesis.Status != epistemic.HypothesisRejected && hypothesis.Status != epistemic.HypothesisSuperseded {
				kind, relevant = epistemic.ObjectHypothesis, true
			}
		}
		for _, frame := range state.Frames {
			if frame.ID == relation.TargetID && frame.ID == state.Task.ActiveFrame && frame.Status == epistemic.FrameActive {
				kind, relevant = epistemic.ObjectFrame, true
			}
		}
		for _, action := range state.Actions {
			if action.ID == relation.TargetID {
				kind, relevant = epistemic.ObjectAction, true
			}
		}
		for _, outcome := range state.Outcomes {
			if outcome.ID == relation.TargetID {
				kind, relevant = epistemic.ObjectOutcome, true
			}
		}
		if !relevant {
			continue
		}
		reason := evidenceSummary(state, relation.SourceID)
		if reason == "" {
			reason = "active contradiction recorded"
		}
		return relation.TargetID, kind, reason, true
	}
	return "", "", "", false
}

func evidenceSummary(state epistemic.State, id epistemic.ID) string {
	for _, value := range state.Observations {
		if value.ID == id {
			return strings.TrimSpace(value.Content)
		}
	}
	for _, value := range state.Claims {
		if value.ID == id {
			return strings.TrimSpace(value.Text)
		}
	}
	return ""
}
