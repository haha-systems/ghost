package epistemic

func canCreateObject(phase Phase, kind ObjectKind) bool {
	switch phase {
	case PhaseTriage:
		return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown
	case PhaseAbduce:
		return kind == ObjectClaim || kind == ObjectHypothesis
	case PhaseFrame:
		return kind == ObjectConstraint || kind == ObjectFrame
	case PhaseExecute:
		return kind == ObjectObservation || kind == ObjectAction || kind == ObjectOutcome
	case PhaseClose:
		return kind == ObjectObservation || kind == ObjectClaim
	default:
		return false
	}
}

func canAssertRelation(phase Phase, kind RelationKind) bool {
	return CanAssertRelation(phase, kind)
}

func objectKind(state State, id ID) (ObjectKind, bool) {
	for _, value := range state.Observations {
		if value.ID == id {
			return ObjectObservation, true
		}
	}
	for _, value := range state.Claims {
		if value.ID == id {
			return ObjectClaim, true
		}
	}
	for _, value := range state.Hypotheses {
		if value.ID == id {
			return ObjectHypothesis, true
		}
	}
	for _, value := range state.Unknowns {
		if value.ID == id {
			return ObjectUnknown, true
		}
	}
	for _, value := range state.Constraints {
		if value.ID == id {
			return ObjectConstraint, true
		}
	}
	for _, value := range state.Frames {
		if value.ID == id {
			return ObjectFrame, true
		}
	}
	for _, value := range state.Actions {
		if value.ID == id {
			return ObjectAction, true
		}
	}
	for _, value := range state.Outcomes {
		if value.ID == id {
			return ObjectOutcome, true
		}
	}
	for _, value := range state.Relations {
		if value.ID == id {
			return ObjectRelation, true
		}
	}
	return "", false
}

func hasObject(state State, id ID) bool {
	_, ok := objectKind(state, id)
	return ok
}

func projected(projection Projection, id ID) bool {
	for _, included := range projection.IDs {
		if included == id {
			return true
		}
	}
	return false
}
