package epistemic

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Store struct {
	state  State
	events []Event
}

func NewStore(goal string) (*Store, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil, errors.New("task goal is empty")
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	task := Task{ID: id, Goal: goal, Status: WorkActive, Phase: PhaseTriage, CreatedAt: now, UpdatedAt: now}
	store := &Store{}
	if err := store.append(Event{Kind: EventTaskCreated, ObjectKind: "task", ObjectID: id, At: now, Payload: mustJSON(task)}); err != nil {
		return nil, err
	}
	return store, nil
}

func Replay(events []Event) (*Store, error) {
	store := &Store{}
	for i, event := range events {
		if event.Sequence != uint64(i+1) {
			return nil, fmt.Errorf("event sequence %d at position %d", event.Sequence, i+1)
		}
		if err := store.apply(event); err != nil {
			return nil, fmt.Errorf("replay event %d: %w", event.Sequence, err)
		}
	}
	if store.state.Task.ID == "" {
		return nil, errors.New("event stream has no task")
	}
	return store, nil
}

func (s *Store) Commit(request CommitRequest) (CommitResult, error) {
	if request.Phase != s.state.Task.Phase || s.state.Task.Status != WorkActive {
		return CommitResult{}, fmt.Errorf("phase %q is not active", request.Phase)
	}
	if request.Producer.Phase != request.Phase || strings.TrimSpace(request.Producer.Process) == "" || strings.TrimSpace(request.Producer.Artifact) == "" {
		return CommitResult{}, errors.New("producer phase, process, and artifact are required")
	}
	if request.Projection.Phase != "" && request.Projection.Phase != request.Phase {
		return CommitResult{}, errors.New("projection phase does not match commit phase")
	}
	if request.Delta.WorkStatus != nil || request.Delta.TerminalReason != "" {
		return CommitResult{}, errors.New("work status is controlled by the orchestrator")
	}
	if deltaEmpty(request.Delta) {
		return CommitResult{}, errors.New("empty delta")
	}
	revision, err := newID()
	if err != nil {
		return CommitResult{}, err
	}
	now := time.Now().UTC()
	result := CommitResult{IDs: map[string]ID{}}
	localKinds := map[string]ObjectKind{}
	localIDs := map[string]ID{}
	localObjects := map[ID]ObjectKind{}
	createdEvents := make([]Event, 0)
	checkRef := func(kind ObjectKind, ref string) error {
		ref = strings.TrimSpace(ref)
		if ref == "" || strings.HasPrefix(ref, "ces_") {
			return fmt.Errorf("invalid local ref %q", ref)
		}
		if _, exists := localKinds[ref]; exists {
			return fmt.Errorf("duplicate local ref %q", ref)
		}
		if !canCreateObject(request.Phase, kind) {
			return fmt.Errorf("phase %q cannot create %s", request.Phase, kind)
		}
		localKinds[ref] = kind
		return nil
	}
	producer := request.Producer

	for _, in := range request.Delta.Observations {
		if err := checkRef(ObjectObservation, in.LocalRef); err != nil {
			return CommitResult{}, err
		}
		if in.DerivedFrom != "" && (!producer.Deterministic || producer.Transform == "" || producer.Version == "") {
			return CommitResult{}, errors.New("derived observation requires deterministic transform provenance")
		}
		id, err := newID()
		if err != nil {
			return CommitResult{}, err
		}
		localKinds[in.LocalRef], localIDs[in.LocalRef], localObjects[id] = ObjectObservation, id, ObjectObservation
		result.IDs[in.LocalRef] = id
		value := Observation{ID: id, Content: in.Content, ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		payload := ObjectCreatedPayload{Kind: ObjectObservation, Object: mustJSON(value)}
		createdEvents = append(createdEvents, Event{Kind: EventObjectCreated, ObjectKind: ObjectObservation, ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(payload)})
	}
	for _, in := range request.Delta.Claims {
		if err := checkRef(ObjectClaim, in.LocalRef); err != nil {
			return CommitResult{}, err
		}
		if strings.TrimSpace(in.Text) == "" {
			return CommitResult{}, errors.New("claim text is empty")
		}
		id, err := newID()
		if err != nil {
			return CommitResult{}, err
		}
		localKinds[in.LocalRef], localIDs[in.LocalRef], localObjects[id] = ObjectClaim, id, ObjectClaim
		result.IDs[in.LocalRef] = id
		value := Claim{ID: id, Text: in.Text, ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		payload := ObjectCreatedPayload{Kind: ObjectClaim, Object: mustJSON(value)}
		createdEvents = append(createdEvents, Event{Kind: EventObjectCreated, ObjectKind: ObjectClaim, ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(payload)})
	}
	for _, in := range request.Delta.Hypotheses {
		if err := checkRef(ObjectHypothesis, in.LocalRef); err != nil {
			return CommitResult{}, err
		}
		if strings.TrimSpace(in.Mechanism) == "" {
			return CommitResult{}, errors.New("hypothesis mechanism is empty")
		}
		id, err := newID()
		if err != nil {
			return CommitResult{}, err
		}
		localKinds[in.LocalRef], localIDs[in.LocalRef], localObjects[id] = ObjectHypothesis, id, ObjectHypothesis
		result.IDs[in.LocalRef] = id
		value := Hypothesis{ID: id, Mechanism: in.Mechanism, Falsifier: in.Falsifier, Status: HypothesisProposed, ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		payload := ObjectCreatedPayload{Kind: ObjectHypothesis, Object: mustJSON(value)}
		createdEvents = append(createdEvents, Event{Kind: EventObjectCreated, ObjectKind: ObjectHypothesis, ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(payload)})
	}
	for _, in := range request.Delta.Unknowns {
		if err := checkRef(ObjectUnknown, in.LocalRef); err != nil {
			return CommitResult{}, err
		}
		if strings.TrimSpace(in.Question) == "" {
			return CommitResult{}, errors.New("unknown question is empty")
		}
		id, err := newID()
		if err != nil {
			return CommitResult{}, err
		}
		localKinds[in.LocalRef], localIDs[in.LocalRef], localObjects[id] = ObjectUnknown, id, ObjectUnknown
		result.IDs[in.LocalRef] = id
		value := Unknown{ID: id, Question: in.Question, Status: UnknownOpen, ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		payload := ObjectCreatedPayload{Kind: ObjectUnknown, Object: mustJSON(value)}
		createdEvents = append(createdEvents, Event{Kind: EventObjectCreated, ObjectKind: ObjectUnknown, ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(payload)})
	}
	for _, in := range request.Delta.Constraints {
		if err := checkRef(ObjectConstraint, in.LocalRef); err != nil {
			return CommitResult{}, err
		}
		if strings.TrimSpace(in.Text) == "" {
			return CommitResult{}, errors.New("constraint text is empty")
		}
		id, err := newID()
		if err != nil {
			return CommitResult{}, err
		}
		localKinds[in.LocalRef], localIDs[in.LocalRef], localObjects[id] = ObjectConstraint, id, ObjectConstraint
		result.IDs[in.LocalRef] = id
		value := Constraint{ID: id, Text: in.Text, ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		payload := ObjectCreatedPayload{Kind: ObjectConstraint, Object: mustJSON(value)}
		createdEvents = append(createdEvents, Event{Kind: EventObjectCreated, ObjectKind: ObjectConstraint, ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(payload)})
	}
	for _, in := range request.Delta.Frames {
		if err := checkRef(ObjectFrame, in.LocalRef); err != nil {
			return CommitResult{}, err
		}
		if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Summary) == "" {
			return CommitResult{}, errors.New("frame name and summary are required")
		}
		id, err := newID()
		if err != nil {
			return CommitResult{}, err
		}
		localKinds[in.LocalRef], localIDs[in.LocalRef], localObjects[id] = ObjectFrame, id, ObjectFrame
		result.IDs[in.LocalRef] = id
		value := Frame{ID: id, Name: in.Name, Summary: in.Summary, CompletionConditions: append([]string(nil), in.CompletionConditions...), DisconfirmationConditions: append([]string(nil), in.DisconfirmationConditions...), Status: FrameActive, ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		payload := ObjectCreatedPayload{Kind: ObjectFrame, Object: mustJSON(value)}
		createdEvents = append(createdEvents, Event{Kind: EventObjectCreated, ObjectKind: ObjectFrame, ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(payload)})
	}
	for _, in := range request.Delta.Actions {
		if err := checkRef(ObjectAction, in.LocalRef); err != nil {
			return CommitResult{}, err
		}
		if strings.TrimSpace(in.Description) == "" {
			return CommitResult{}, errors.New("action description is empty")
		}
		id, err := newID()
		if err != nil {
			return CommitResult{}, err
		}
		localKinds[in.LocalRef], localIDs[in.LocalRef], localObjects[id] = ObjectAction, id, ObjectAction
		result.IDs[in.LocalRef] = id
		value := Action{ID: id, Description: in.Description, Status: "recorded", ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		payload := ObjectCreatedPayload{Kind: ObjectAction, Object: mustJSON(value)}
		createdEvents = append(createdEvents, Event{Kind: EventObjectCreated, ObjectKind: ObjectAction, ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(payload)})
	}
	for _, in := range request.Delta.Outcomes {
		if err := checkRef(ObjectOutcome, in.LocalRef); err != nil {
			return CommitResult{}, err
		}
		if strings.TrimSpace(in.Description) == "" {
			return CommitResult{}, errors.New("outcome description is empty")
		}
		id, err := newID()
		if err != nil {
			return CommitResult{}, err
		}
		localKinds[in.LocalRef], localIDs[in.LocalRef], localObjects[id] = ObjectOutcome, id, ObjectOutcome
		result.IDs[in.LocalRef] = id
		value := Outcome{ID: id, Description: in.Description, ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		payload := ObjectCreatedPayload{Kind: ObjectOutcome, Object: mustJSON(value)}
		createdEvents = append(createdEvents, Event{Kind: EventObjectCreated, ObjectKind: ObjectOutcome, ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(payload)})
	}

	resolve := func(ref string) (ID, ObjectKind, error) {
		if id, ok := localIDs[ref]; ok {
			return id, localKinds[ref], nil
		}
		id := ID(ref)
		kind, ok := objectKind(s.state, id)
		if !ok {
			return "", "", fmt.Errorf("reference %q does not exist", ref)
		}
		if !projected(request.Projection, id) {
			return "", "", fmt.Errorf("reference %q is outside the phase projection", ref)
		}
		return id, kind, nil
	}
	allKinds := func(id ID) (ObjectKind, bool) {
		if kind, ok := localObjects[id]; ok {
			return kind, true
		}
		return objectKind(s.state, id)
	}
	newRelations := make([]Relation, 0)
	relationEvents := make([]Event, 0)
	addRelation := func(ref string, kind RelationKind, sourceRef, targetRef string) error {
		if strings.TrimSpace(ref) == "" || strings.HasPrefix(ref, "ces_") {
			return fmt.Errorf("invalid relation local ref %q", ref)
		}
		if _, exists := localKinds[ref]; exists {
			return fmt.Errorf("duplicate local ref %q", ref)
		}
		if !canAssertRelation(request.Phase, kind) {
			return fmt.Errorf("phase %q cannot assert relation %q", request.Phase, kind)
		}
		source, sourceKind, err := resolve(sourceRef)
		if err != nil {
			return err
		}
		target, targetKind, err := resolve(targetRef)
		if err != nil {
			return err
		}
		if !validRelationEndpoints(kind, sourceKind, targetKind) {
			return fmt.Errorf("relation %q cannot connect %s to %s", kind, sourceKind, targetKind)
		}
		id, err := newID()
		if err != nil {
			return err
		}
		relation := Relation{ID: id, Kind: kind, SourceID: source, TargetID: target, Status: RelationActive, ProducedBy: producer, RevisionID: revision, CreatedAt: now}
		localKinds[ref], localIDs[ref] = "relation", id
		result.IDs[ref] = id
		newRelations = append(newRelations, relation)
		relationEvents = append(relationEvents, Event{Kind: EventRelationCreated, ObjectKind: "relation", ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(relation)})
		return nil
	}
	for _, in := range request.Delta.Observations {
		if in.DerivedFrom == "" {
			continue
		}
		sourceID, sourceKind, err := resolve(in.DerivedFrom)
		if err != nil {
			return CommitResult{}, err
		}
		if sourceKind != ObjectObservation {
			return CommitResult{}, errors.New("derived observation source must be an observation")
		}
		if err := addRelation("derived_"+in.LocalRef, RelationDerivedFrom, in.LocalRef, in.DerivedFrom); err != nil {
			return CommitResult{}, err
		}
		_ = sourceID
	}
	for _, in := range request.Delta.Relations {
		if err := addRelation(in.LocalRef, in.Kind, in.Source, in.Target); err != nil {
			return CommitResult{}, err
		}
	}
	for _, claim := range request.Delta.Claims {
		claimID := localIDs[claim.LocalRef]
		supported := false
		for _, relation := range newRelations {
			if relation.Kind == RelationSupports && relation.TargetID == claimID {
				sourceKind, _ := allKinds(relation.SourceID)
				supported = sourceKind == ObjectObservation || sourceKind == ObjectClaim
			}
		}
		for _, relation := range s.state.Relations {
			if relation.Status == RelationActive && relation.Kind == RelationSupports && relation.TargetID == claimID {
				supported = true
			}
		}
		if !supported {
			return CommitResult{}, fmt.Errorf("claim %q has no supporting source", claim.LocalRef)
		}
	}

	if request.Delta.LeadingHypothesis != "" {
		if request.Phase != PhaseAbduce {
			return CommitResult{}, errors.New("only abduction may select a leading hypothesis")
		}
		id, kind, err := resolve(request.Delta.LeadingHypothesis)
		if err != nil {
			return CommitResult{}, err
		}
		if kind != ObjectHypothesis {
			return CommitResult{}, errors.New("leading reference is not a hypothesis")
		}
		if s.state.Task.LeadingHypothesis != "" && s.state.Task.LeadingHypothesis != id {
			if err := s.prepareStatusEvent(&createdEvents, revision, now, ObjectHypothesis, s.state.Task.LeadingHypothesis, string(HypothesisLive)); err != nil {
				return CommitResult{}, err
			}
		}
		if err := s.prepareStatusEvent(&createdEvents, revision, now, ObjectHypothesis, id, string(HypothesisLeading)); err != nil {
			return CommitResult{}, err
		}
	}
	if request.Delta.ActiveFrame != "" {
		if request.Phase != PhaseFrame {
			return CommitResult{}, errors.New("only framing may activate a frame")
		}
		id, kind, err := resolve(request.Delta.ActiveFrame)
		if err != nil {
			return CommitResult{}, err
		}
		if kind != ObjectFrame || localKinds[request.Delta.ActiveFrame] != ObjectFrame {
			return CommitResult{}, errors.New("active frame must be created in this commit")
		}
		if s.state.Task.ActiveFrame != "" {
			found := false
			for _, relation := range newRelations {
				if relation.Kind == RelationSupersedes && relation.SourceID == id && relation.TargetID == s.state.Task.ActiveFrame {
					found = true
				}
			}
			if !found {
				return CommitResult{}, errors.New("new frame must supersede the active frame")
			}
			if err := s.prepareStatusEvent(&createdEvents, revision, now, ObjectFrame, s.state.Task.ActiveFrame, string(FrameSuperseded)); err != nil {
				return CommitResult{}, err
			}
		}
	}
	for _, change := range request.Delta.StatusChanges {
		id, kind, err := resolve(change.Ref)
		if err != nil {
			return CommitResult{}, err
		}
		if kind != change.Kind {
			return CommitResult{}, fmt.Errorf("status reference %q is %s, not %s", change.Ref, kind, change.Kind)
		}
		if err := validateStatusChange(request.Phase, s.state, kind, id, change.Status); err != nil {
			return CommitResult{}, err
		}
		if kind == ObjectObservation {
			return CommitResult{}, errors.New("observations are immutable")
		}
		if kind != ObjectHypothesis && kind != ObjectUnknown && kind != ObjectFrame {
			return CommitResult{}, fmt.Errorf("status changes are not supported for %s", kind)
		}
		if err := s.prepareStatusEvent(&createdEvents, revision, now, kind, id, change.Status); err != nil {
			return CommitResult{}, err
		}
	}
	for _, relation := range newRelations {
		if relation.Kind != RelationResolves {
			continue
		}
		if err := s.prepareStatusEvent(&createdEvents, revision, now, ObjectUnknown, relation.TargetID, string(UnknownResolved)); err != nil {
			return CommitResult{}, err
		}
	}

	retractionEvents := make([]Event, 0)
	for _, input := range request.Delta.Retractions {
		id, kind, err := resolve(input.Relation)
		if err != nil {
			return CommitResult{}, err
		}
		if kind != "relation" {
			return CommitResult{}, errors.New("retraction target is not a relation")
		}
		var relation *Relation
		for i := range s.state.Relations {
			if s.state.Relations[i].ID == id {
				relation = &s.state.Relations[i]
				break
			}
		}
		if relation == nil || relation.Status != RelationActive {
			return CommitResult{}, errors.New("relation is not active")
		}
		if !canAssertRelation(request.Phase, relation.Kind) {
			return CommitResult{}, fmt.Errorf("phase %q cannot retract relation %q", request.Phase, relation.Kind)
		}
		var reason ID
		if input.ReasonRef != "" {
			var reasonKind ObjectKind
			reason, reasonKind, err = resolve(input.ReasonRef)
			if err != nil {
				return CommitResult{}, err
			}
			if reasonKind != ObjectClaim {
				return CommitResult{}, errors.New("retraction reason must be a projected claim")
			}
		}
		if relation.Kind == RelationContradicts && reason == "" {
			return CommitResult{}, errors.New("contradiction retraction requires a projected reason claim")
		}
		retractionEvents = append(retractionEvents, Event{Kind: EventRelationStatusChanged, ObjectKind: "relation", ObjectID: id, RevisionID: revision, At: now, Payload: mustJSON(relationStatusPayload{ID: id, From: relation.Status, To: RelationRetracted, ReasonRef: reason})})
	}

	// Nothing reaches the event log until all references and permissions pass.
	for _, event := range createdEvents {
		if event.Kind == EventObjectCreated {
			if err := s.append(event); err != nil {
				return CommitResult{}, err
			}
		}
	}
	for _, event := range relationEvents {
		if err := s.append(event); err != nil {
			return CommitResult{}, err
		}
	}
	for _, event := range createdEvents {
		if event.Kind != EventObjectCreated {
			if err := s.append(event); err != nil {
				return CommitResult{}, err
			}
		}
	}
	for _, event := range retractionEvents {
		if err := s.append(event); err != nil {
			return CommitResult{}, err
		}
	}
	for _, event := range createdEvents {
		if event.ObjectID != "" {
			result.Events = append(result.Events, event)
		}
	}
	for _, event := range relationEvents {
		result.Events = append(result.Events, event)
	}
	for _, event := range retractionEvents {
		result.Events = append(result.Events, event)
	}
	return result, nil
}

func deltaEmpty(delta Delta) bool {
	return len(delta.Observations)+len(delta.Claims)+len(delta.Hypotheses)+len(delta.Unknowns)+len(delta.Constraints)+len(delta.Frames)+len(delta.Actions)+len(delta.Outcomes)+len(delta.Relations)+len(delta.Retractions)+len(delta.StatusChanges) == 0 && delta.LeadingHypothesis == "" && delta.ActiveFrame == "" && delta.WorkStatus == nil && delta.TerminalReason == ""
}

func validRelationEndpoints(kind RelationKind, source, target ObjectKind) bool {
	switch kind {
	case RelationDerivedFrom:
		return source == ObjectObservation && target == ObjectObservation
	case RelationSupports, RelationContradicts:
		return (source == ObjectObservation || source == ObjectClaim) && (target == ObjectClaim || target == ObjectHypothesis || target == ObjectFrame || target == ObjectAction || target == ObjectOutcome)
	case RelationDependsOn:
		return source == ObjectFrame && (target == ObjectHypothesis || target == ObjectConstraint || target == ObjectFrame)
	case RelationImplements:
		return source == ObjectAction && (target == ObjectFrame || target == ObjectConstraint || target == ObjectHypothesis)
	case RelationTests:
		return (source == ObjectObservation || source == ObjectClaim || source == ObjectOutcome) && (target == ObjectAction || target == ObjectOutcome || target == ObjectHypothesis || target == ObjectFrame)
	case RelationResolves:
		return (source == ObjectObservation || source == ObjectClaim) && target == ObjectUnknown
	case RelationSupersedes:
		return (source == ObjectFrame && target == ObjectFrame) || (source == ObjectHypothesis && target == ObjectHypothesis)
	default:
		return false
	}
}

func (s *Store) prepareStatusEvent(events *[]Event, revision ID, at time.Time, kind ObjectKind, id ID, to string) error {
	var from string
	switch kind {
	case ObjectHypothesis:
		for _, value := range s.state.Hypotheses {
			if value.ID == id {
				from = string(value.Status)
				break
			}
		}
	case ObjectUnknown:
		for _, value := range s.state.Unknowns {
			if value.ID == id {
				from = string(value.Status)
				break
			}
		}
	case ObjectFrame:
		for _, value := range s.state.Frames {
			if value.ID == id {
				from = string(value.Status)
				break
			}
		}
	default:
		return fmt.Errorf("status changes are not supported for %s", kind)
	}
	if from == "" {
		for _, event := range *events {
			if event.Kind != EventObjectCreated || event.ObjectID != id {
				continue
			}
			var payload ObjectCreatedPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return err
			}
			switch kind {
			case ObjectHypothesis:
				var value Hypothesis
				_ = json.Unmarshal(payload.Object, &value)
				from = string(value.Status)
			case ObjectUnknown:
				var value Unknown
				_ = json.Unmarshal(payload.Object, &value)
				from = string(value.Status)
			case ObjectFrame:
				var value Frame
				_ = json.Unmarshal(payload.Object, &value)
				from = string(value.Status)
			}
			break
		}
	}
	if !validStatusTransition(kind, from, to) {
		return fmt.Errorf("invalid %s status transition %q -> %q", kind, from, to)
	}
	eventKind := EventObjectStatusChanged
	if kind == ObjectUnknown {
		eventKind = EventUnknownStatusChanged
	}
	if kind == ObjectFrame {
		eventKind = EventFrameStatusChanged
	}
	*events = append(*events, Event{Kind: eventKind, ObjectKind: kind, ObjectID: id, RevisionID: revision, At: at, Payload: mustJSON(objectStatusPayload{Kind: kind, ID: id, From: from, To: to})})
	return nil
}

func validStatusTransition(kind ObjectKind, from, to string) bool {
	switch kind {
	case ObjectHypothesis:
		allowed := map[string][]string{
			string(HypothesisProposed): {string(HypothesisLive), string(HypothesisLeading), string(HypothesisRejected)},
			string(HypothesisLive):     {string(HypothesisLeading), string(HypothesisWeakened), string(HypothesisRejected), string(HypothesisSuperseded), string(HypothesisConfirmed)},
			string(HypothesisLeading):  {string(HypothesisLive), string(HypothesisWeakened), string(HypothesisRejected), string(HypothesisSuperseded), string(HypothesisConfirmed)},
			string(HypothesisWeakened): {string(HypothesisLive), string(HypothesisLeading), string(HypothesisRejected), string(HypothesisSuperseded)},
		}
		for _, value := range allowed[from] {
			if value == to {
				return true
			}
		}
	case ObjectUnknown:
		return (from == string(UnknownOpen) || from == string(UnknownReopened)) && to == string(UnknownResolved) || from == string(UnknownResolved) && to == string(UnknownReopened)
	case ObjectFrame:
		return from == string(FrameActive) && to == string(FrameSuperseded)
	}
	return false
}

func validateStatusChange(phase Phase, state State, kind ObjectKind, id ID, status string) error {
	if kind == ObjectObservation {
		return errors.New("observations are immutable")
	}
	if kind == ObjectHypothesis && phase != PhaseAbduce && !(phase == PhaseClose && status == string(HypothesisConfirmed)) {
		return errors.New("phase cannot change hypothesis status")
	}
	if kind == ObjectHypothesis && phase == PhaseClose && status == string(HypothesisConfirmed) {
		return nil
	}
	if kind == ObjectUnknown && phase != PhaseTriage && phase != PhaseAbduce && phase != PhaseExecute && phase != PhaseClose {
		return errors.New("phase cannot change unknown status")
	}
	if kind == ObjectFrame {
		return errors.New("frames change status only when superseded by a new frame")
	}
	if kind == ObjectUnknown {
		for _, value := range state.Unknowns {
			if value.ID == id && value.Status == UnknownResolved && status == string(UnknownReopened) && phase == PhaseTriage {
				return nil
			}
		}
	}
	return errors.New("unsupported status change")
}

func (s *Store) State() State { return cloneState(s.state) }

func (s *Store) Events() []Event {
	out := make([]Event, len(s.events))
	for i, event := range s.events {
		out[i] = event
		out[i].Payload = append(json.RawMessage(nil), event.Payload...)
	}
	return out
}

func (s *Store) append(event Event) error {
	event.Sequence = uint64(len(s.events) + 1)
	if event.WorkID == "" {
		if event.Kind == EventTaskCreated {
			event.WorkID = string(event.ObjectID)
		} else {
			event.WorkID = string(s.state.Task.ID)
		}
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if err := s.apply(event); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

func (s *Store) apply(event Event) error {
	if err := applyEvent(&s.state, event); err != nil {
		return err
	}
	return nil
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
