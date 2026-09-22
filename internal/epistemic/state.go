package epistemic

import (
	"encoding/json"
	"fmt"
)

func applyEvent(state *State, event Event) error {
	switch event.Kind {
	case EventTaskCreated:
		if state.Task.ID != "" {
			return fmt.Errorf("duplicate task event")
		}
		if err := json.Unmarshal(event.Payload, &state.Task); err != nil {
			return fmt.Errorf("decode task event: %w", err)
		}
	case EventObjectCreated:
		var payload ObjectCreatedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode object event: %w", err)
		}
		appendObject := func(dst any) error { return json.Unmarshal(payload.Object, dst) }
		switch payload.Kind {
		case ObjectObservation:
			var v Observation
			if err := appendObject(&v); err != nil {
				return err
			}
			state.Observations = append(state.Observations, v)
		case ObjectClaim:
			var v Claim
			if err := appendObject(&v); err != nil {
				return err
			}
			state.Claims = append(state.Claims, v)
		case ObjectHypothesis:
			var v Hypothesis
			if err := appendObject(&v); err != nil {
				return err
			}
			state.Hypotheses = append(state.Hypotheses, v)
		case ObjectUnknown:
			var v Unknown
			if err := appendObject(&v); err != nil {
				return err
			}
			state.Unknowns = append(state.Unknowns, v)
		case ObjectConstraint:
			var v Constraint
			if err := appendObject(&v); err != nil {
				return err
			}
			state.Constraints = append(state.Constraints, v)
		case ObjectFrame:
			var v Frame
			if err := appendObject(&v); err != nil {
				return err
			}
			state.Frames = append(state.Frames, v)
			if v.Status == FrameActive {
				state.Task.ActiveFrame = v.ID
			}
		case ObjectAction:
			var v Action
			if err := appendObject(&v); err != nil {
				return err
			}
			state.Actions = append(state.Actions, v)
		case ObjectOutcome:
			var v Outcome
			if err := appendObject(&v); err != nil {
				return err
			}
			state.Outcomes = append(state.Outcomes, v)
		default:
			return fmt.Errorf("unknown object kind %q", payload.Kind)
		}
	case EventRelationCreated:
		var v Relation
		if err := json.Unmarshal(event.Payload, &v); err != nil {
			return fmt.Errorf("decode relation event: %w", err)
		}
		state.Relations = append(state.Relations, v)
	case EventObjectStatusChanged, EventUnknownStatusChanged, EventFrameStatusChanged:
		var payload objectStatusPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode object status event: %w", err)
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
	case EventRelationStatusChanged:
		var payload relationStatusPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode relation status event: %w", err)
		}
		for i := range state.Relations {
			if state.Relations[i].ID == payload.ID {
				state.Relations[i].Status = payload.To
				state.Relations[i].ReasonRef = payload.ReasonRef
				return nil
			}
		}
		return fmt.Errorf("relation status event refers to missing relation %q", payload.ID)
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

func cloneState(state State) State {
	data, _ := json.Marshal(state)
	var copy State
	_ = json.Unmarshal(data, &copy)
	return copy
}
