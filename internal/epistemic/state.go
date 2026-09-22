package epistemic

import (
	"encoding/json"
	"fmt"
)

func applyEvent(state *State, event Event) error {
	switch event.Kind {
	case EventTaskCreated:
		return applyTaskCreated(state, event)
	case EventObjectCreated:
		return applyObjectCreated(state, event)
	case EventRelationCreated:
		return applyRelationCreated(state, event)
	case EventObjectStatusChanged, EventUnknownStatusChanged, EventFrameStatusChanged:
		return applyObjectStatusChanged(state, event)
	case EventRelationStatusChanged:
		return applyRelationStatusChanged(state, event)
	case EventPhaseChanged:
		var payload phasePayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return err
		}
		state.Task.Phase = payload.To
		state.Task.ReopenCount = payload.ReopenCount
		state.Task.UpdatedAt = event.At
		state.Task.LastTransition = &TransitionSummary{From: payload.From, To: payload.To, Reason: payload.Reason, TargetID: payload.TargetID, TargetKind: payload.TargetKind}
	case EventWorkStatusChanged:
		var payload workStatusPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return err
		}
		state.Task.Status = payload.To
		state.Task.TerminalReason = payload.Reason
		state.Task.UpdatedAt = event.At
	case EventTerminalReasonRecorded:
		var reason string
		if err := json.Unmarshal(event.Payload, &reason); err != nil {
			return err
		}
		state.Task.TerminalReason = reason
		state.Task.UpdatedAt = event.At
	default:
		return fmt.Errorf("unknown event kind %q", event.Kind)
	}
	return nil
}

func applyTaskCreated(state *State, event Event) error {
	if state.Task.ID != "" {
		return fmt.Errorf("duplicate task event")
	}
	if err := json.Unmarshal(event.Payload, &state.Task); err != nil {
		return fmt.Errorf("decode task event: %w", err)
	}
	return nil
}

func applyObjectCreated(state *State, event Event) error {
	var payload ObjectCreatedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode object event: %w", err)
	}
	appendObject := func(dst any) error { return json.Unmarshal(payload.Object, dst) }
	switch payload.Kind {
	case ObjectObservation:
		var value Observation
		if err := appendObject(&value); err != nil {
			return err
		}
		state.Observations = append(state.Observations, value)
	case ObjectClaim:
		var value Claim
		if err := appendObject(&value); err != nil {
			return err
		}
		state.Claims = append(state.Claims, value)
	case ObjectHypothesis:
		var value Hypothesis
		if err := appendObject(&value); err != nil {
			return err
		}
		state.Hypotheses = append(state.Hypotheses, value)
	case ObjectUnknown:
		var value Unknown
		if err := appendObject(&value); err != nil {
			return err
		}
		state.Unknowns = append(state.Unknowns, value)
	case ObjectConstraint:
		var value Constraint
		if err := appendObject(&value); err != nil {
			return err
		}
		state.Constraints = append(state.Constraints, value)
	case ObjectFrame:
		var value Frame
		if err := appendObject(&value); err != nil {
			return err
		}
		state.Frames = append(state.Frames, value)
		if value.Status == FrameActive {
			state.Task.ActiveFrame = value.ID
		}
	case ObjectAction:
		var value Action
		if err := appendObject(&value); err != nil {
			return err
		}
		state.Actions = append(state.Actions, value)
	case ObjectOutcome:
		var value Outcome
		if err := appendObject(&value); err != nil {
			return err
		}
		state.Outcomes = append(state.Outcomes, value)
	default:
		return fmt.Errorf("unknown object kind %q", payload.Kind)
	}
	return nil
}

func applyRelationCreated(state *State, event Event) error {
	var value Relation
	if err := json.Unmarshal(event.Payload, &value); err != nil {
		return fmt.Errorf("decode relation event: %w", err)
	}
	state.Relations = append(state.Relations, value)
	return nil
}

func applyObjectStatusChanged(state *State, event Event) error {
	var payload objectStatusPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	switch payload.Kind {
	case ObjectHypothesis:
		for i := range state.Hypotheses {
			if state.Hypotheses[i].ID == payload.ID {
				state.Hypotheses[i].Status = HypothesisStatus(payload.To)
				if payload.To == string(HypothesisLeading) {
					state.Task.LeadingHypothesis = payload.ID
				}
				if payload.From == string(HypothesisLeading) && payload.To != string(HypothesisLeading) && payload.To != string(HypothesisConfirmed) {
					state.Task.LeadingHypothesis = ""
				}
				return nil
			}
		}
	case ObjectUnknown:
		for i := range state.Unknowns {
			if state.Unknowns[i].ID == payload.ID {
				state.Unknowns[i].Status = UnknownStatus(payload.To)
				return nil
			}
		}
	case ObjectFrame:
		for i := range state.Frames {
			if state.Frames[i].ID == payload.ID {
				state.Frames[i].Status = FrameStatus(payload.To)
				if payload.To == string(FrameActive) {
					state.Task.ActiveFrame = payload.ID
				}
				if payload.From == string(FrameActive) && payload.To != string(FrameActive) && state.Task.ActiveFrame == payload.ID {
					state.Task.ActiveFrame = ""
				}
				return nil
			}
		}
	}
	return fmt.Errorf("status event refers to missing %s %q", payload.Kind, payload.ID)
}

func applyRelationStatusChanged(state *State, event Event) error {
	var payload relationStatusPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	for i := range state.Relations {
		if state.Relations[i].ID == payload.ID {
			state.Relations[i].Status = payload.To
			state.Relations[i].ReasonRef = payload.ReasonRef
			return nil
		}
	}
	return fmt.Errorf("relation status event refers to missing relation %q", payload.ID)
}

func cloneState(state State) State {
	data, _ := json.Marshal(state)
	var copy State
	_ = json.Unmarshal(data, &copy)
	return copy
}
