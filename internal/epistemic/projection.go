package epistemic

import (
	"encoding/json"
	"fmt"
	"sort"
)

func (s *Store) Project(request ProjectionRequest) (Projection, error) {
	if request.Phase != s.state.Task.Phase || s.state.Task.Status != WorkActive {
		return Projection{}, fmt.Errorf("phase %q is not active", request.Phase)
	}
	all := cloneState(s.state)
	kinds := map[ID]ObjectKind{}
	for _, value := range all.Observations {
		kinds[value.ID] = ObjectObservation
	}
	for _, value := range all.Claims {
		kinds[value.ID] = ObjectClaim
	}
	for _, value := range all.Hypotheses {
		kinds[value.ID] = ObjectHypothesis
	}
	for _, value := range all.Unknowns {
		kinds[value.ID] = ObjectUnknown
	}
	for _, value := range all.Constraints {
		kinds[value.ID] = ObjectConstraint
	}
	for _, value := range all.Frames {
		kinds[value.ID] = ObjectFrame
	}
	for _, value := range all.Actions {
		kinds[value.ID] = ObjectAction
	}
	for _, value := range all.Outcomes {
		kinds[value.ID] = ObjectOutcome
	}
	selected := map[ID]bool{}
	add := func(id ID) error {
		kind, ok := kinds[id]
		if !ok {
			return fmt.Errorf("projection object %q does not exist", id)
		}
		if !phaseCanSee(request.Phase, kind) {
			return fmt.Errorf("phase %q cannot see %s", request.Phase, kind)
		}
		selected[id] = true
		return nil
	}
	if len(request.Include) != 0 {
		for _, id := range request.Include {
			if err := add(id); err != nil {
				return Projection{}, err
			}
		}
	} else {
		for id, kind := range kinds {
			if defaultVisible(all, request.Phase, id, kind) {
				selected[id] = true
			}
		}
	}

	// Evidence attached to a selected proposition travels with that proposition.
	changed := true
	for changed {
		changed = false
		for _, relation := range all.Relations {
			if relation.Status != RelationActive {
				continue
			}
			if selected[relation.TargetID] && (relation.Kind == RelationContradicts || relation.Kind == RelationSupports || relation.Kind == RelationResolves || relation.Kind == RelationTests) && !selected[relation.SourceID] {
				kind := kinds[relation.SourceID]
				if phaseCanSee(request.Phase, kind) {
					selected[relation.SourceID], changed = true, true
				}
			}
			if selected[relation.SourceID] && relation.Kind == RelationDerivedFrom && !selected[relation.TargetID] {
				kind := kinds[relation.TargetID]
				if phaseCanSee(request.Phase, kind) {
					selected[relation.TargetID], changed = true, true
				}
			}
		}
	}

	view := State{Task: all.Task}
	if !selected[view.Task.LeadingHypothesis] {
		view.Task.LeadingHypothesis = ""
	}
	if !selected[view.Task.ActiveFrame] {
		view.Task.ActiveFrame = ""
	}
	if view.Task.LastTransition != nil && !selected[view.Task.LastTransition.TargetID] {
		view.Task.LastTransition.TargetID = ""
	}
	view.Observations = filter(all.Observations, func(v Observation) bool { return selected[v.ID] })
	view.Claims = filter(all.Claims, func(v Claim) bool { return selected[v.ID] })
	view.Hypotheses = filter(all.Hypotheses, func(v Hypothesis) bool { return selected[v.ID] })
	view.Unknowns = filter(all.Unknowns, func(v Unknown) bool { return selected[v.ID] })
	view.Constraints = filter(all.Constraints, func(v Constraint) bool { return selected[v.ID] })
	view.Frames = filter(all.Frames, func(v Frame) bool { return selected[v.ID] })
	view.Actions = filter(all.Actions, func(v Action) bool { return selected[v.ID] })
	view.Outcomes = filter(all.Outcomes, func(v Outcome) bool { return selected[v.ID] })
	ids := make([]ID, 0, len(selected)+len(all.Relations))
	for id := range selected {
		ids = append(ids, id)
	}
	for _, relation := range all.Relations {
		if selected[relation.SourceID] && selected[relation.TargetID] {
			view.Relations = append(view.Relations, relation)
			ids = append(ids, relation.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return Projection{Phase: request.Phase, State: view, IDs: ids}, nil
}

func phaseCanSee(phase Phase, kind ObjectKind) bool {
	switch phase {
	case PhaseTriage:
		return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown
	case PhaseAbduce:
		return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown || kind == ObjectHypothesis
	case PhaseFrame:
		return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown || kind == ObjectHypothesis || kind == ObjectConstraint || kind == ObjectFrame
	case PhaseExecute:
		return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown || kind == ObjectHypothesis || kind == ObjectConstraint || kind == ObjectFrame || kind == ObjectAction || kind == ObjectOutcome
	case PhaseClose:
		return kind != ObjectRelation
	default:
		return false
	}
}

func defaultVisible(state State, phase Phase, id ID, kind ObjectKind) bool {
	if !phaseCanSee(phase, kind) {
		return false
	}
	switch phase {
	case PhaseTriage:
		return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown
	case PhaseAbduce:
		if kind != ObjectHypothesis {
			return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown
		}
		for _, value := range state.Hypotheses {
			if value.ID == id {
				return value.Status != HypothesisRejected && value.Status != HypothesisSuperseded
			}
		}
	case PhaseFrame:
		return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown || kind == ObjectConstraint || id == state.Task.LeadingHypothesis || id == state.Task.ActiveFrame
	case PhaseExecute:
		if kind == ObjectFrame {
			return id == state.Task.ActiveFrame
		}
		if kind == ObjectHypothesis {
			for _, relation := range state.Relations {
				if relation.Status == RelationActive && relation.Kind == RelationDependsOn && relation.SourceID == state.Task.ActiveFrame && relation.TargetID == id {
					return true
				}
			}
			return id == state.Task.LeadingHypothesis
		}
		return kind == ObjectObservation || kind == ObjectClaim || kind == ObjectUnknown || kind == ObjectConstraint || kind == ObjectAction || kind == ObjectOutcome
	case PhaseClose:
		return true
	}
	return false
}

func filter[T any](input []T, keep func(T) bool) []T {
	if len(input) == 0 {
		return nil
	}
	output := make([]T, 0, len(input))
	for _, value := range input {
		if keep(value) {
			output = append(output, value)
		}
	}
	return output
}

func (s *Store) OperatorView() OperatorView { return OperatorViewForState(s.state) }

func OperatorViewForState(state State) OperatorView {
	view := OperatorView{WorkID: string(state.Task.ID), Goal: state.Task.Goal, WorkStatus: state.Task.Status, Phase: state.Task.Phase, ReopenCount: state.Task.ReopenCount}
	for _, unknown := range state.Unknowns {
		if unknown.Status == UnknownOpen || unknown.Status == UnknownReopened {
			view.OpenUnknownCount++
		}
	}
	for _, relation := range state.Relations {
		if relation.Kind == RelationContradicts && relation.Status == RelationActive {
			view.ContradictionCount++
		}
	}
	for i, hypothesis := range state.Hypotheses {
		if hypothesis.ID == state.Task.LeadingHypothesis {
			view.LeadingHypothesis = &ObjectSummary{ID: hypothesis.ID, Label: fmt.Sprintf("H%d", i+1), Summary: hypothesis.Mechanism, Status: string(hypothesis.Status)}
			break
		}
	}
	for i, frame := range state.Frames {
		if frame.ID == state.Task.ActiveFrame {
			view.ActiveFrame = &ObjectSummary{ID: frame.ID, Label: fmt.Sprintf("F%d", i+1), Summary: frame.Summary, Status: string(frame.Status)}
			break
		}
	}
	if state.Task.LastTransition != nil {
		transition := *state.Task.LastTransition
		view.LastTransition = &transition
	}
	return view
}

func (s *Store) SemanticEvents() []SemanticEvent { return SemanticEvents(s.Events()) }

func SemanticEvents(events []Event) []SemanticEvent {
	result := make([]SemanticEvent, 0)
	for _, event := range events {
		switch event.Kind {
		case EventPhaseChanged:
			var value phasePayload
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				continue
			}
			result = append(result, SemanticEvent{Kind: SemanticPhaseChanged, At: event.At, From: value.From, To: value.To, WorkID: event.WorkID, ObjectID: value.TargetID, ObjectKind: value.TargetKind, Reason: value.Reason})
			if phaseIndex(value.To) < phaseIndex(value.From) {
				result = append(result, SemanticEvent{Kind: SemanticWorkReopened, At: event.At, From: value.From, To: value.To, WorkID: event.WorkID, ObjectID: value.TargetID, ObjectKind: value.TargetKind, Reason: value.Reason})
			}
		case EventObjectCreated:
			var payload ObjectCreatedPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			if payload.Kind == ObjectFrame {
				result = append(result, SemanticEvent{Kind: SemanticFrameActivated, At: event.At, WorkID: event.WorkID, ObjectID: event.ObjectID, ObjectKind: ObjectFrame})
			}
		case EventRelationCreated:
			var relation Relation
			if err := json.Unmarshal(event.Payload, &relation); err == nil && relation.Kind == RelationContradicts {
				result = append(result, SemanticEvent{Kind: SemanticContradictionRecorded, At: event.At, WorkID: event.WorkID, ObjectID: relation.TargetID, ObjectKind: objectKindFromEvent(events, relation.TargetID)})
			}
		case EventObjectStatusChanged:
			var value objectStatusPayload
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				continue
			}
			if value.Kind == ObjectHypothesis && value.To == string(HypothesisLeading) {
				result = append(result, SemanticEvent{Kind: SemanticHypothesisBecameLeading, At: event.At, ObjectID: value.ID, ObjectKind: ObjectHypothesis})
			}
			if value.Kind == ObjectHypothesis && value.To == string(HypothesisRejected) {
				result = append(result, SemanticEvent{Kind: SemanticHypothesisRejected, At: event.At, ObjectID: value.ID, ObjectKind: ObjectHypothesis})
			}
		case EventWorkStatusChanged:
			var value workStatusPayload
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				continue
			}
			kind := SemanticWorkIncomplete
			if value.To == WorkComplete {
				kind = SemanticWorkCompleted
			}
			result = append(result, SemanticEvent{Kind: kind, At: event.At, WorkID: event.WorkID, Reason: value.Reason})
		}
	}
	return result
}

func objectKindFromEvent(events []Event, id ID) ObjectKind {
	for _, event := range events {
		if event.Kind == EventObjectCreated && event.ObjectID == id {
			return event.ObjectKind
		}
	}
	return ""
}
