package epistemic

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// commitPlanner builds the events for one commit. Store.Commit appends them.
type commitPlanner struct {
	store          *Store
	request        CommitRequest
	revision       ID
	now            time.Time
	result         CommitResult
	localKinds     map[string]ObjectKind
	localIDs       map[string]ID
	localObjects   map[ID]ObjectKind
	created        []Event
	relations      []Relation
	relationEvents []Event
	retractions    []Event
}

func (p *commitPlanner) plan() error {
	steps := []func() error{
		p.planObservations,
		p.planClaims,
		p.planHypotheses,
		p.planUnknowns,
		p.planConstraints,
		p.planFrames,
		p.planActions,
		p.planOutcomes,
		p.planRelations,
		p.planClaimSupport,
		p.planLeadingHypothesis,
		p.planActiveFrame,
		p.planStatusChanges,
		p.planResolvedUnknowns,
		p.planRetractions,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) checkRef(kind ObjectKind, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "ces_") {
		return fmt.Errorf("invalid local ref %q", ref)
	}
	if _, exists := p.localKinds[ref]; exists {
		return fmt.Errorf("duplicate local ref %q", ref)
	}
	if !canCreateObject(p.request.Phase, kind) {
		return fmt.Errorf("phase %q cannot create %s", p.request.Phase, kind)
	}
	p.localKinds[ref] = kind
	return nil
}

func (p *commitPlanner) mint(ref string, kind ObjectKind) (ID, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	p.localKinds[ref], p.localIDs[ref], p.localObjects[id] = kind, id, kind
	p.result.IDs[ref] = id
	return id, nil
}

func (p *commitPlanner) appendCreated(kind ObjectKind, id ID, value any) error {
	objectJSON, err := marshalJSON(value)
	if err != nil {
		return err
	}
	payloadJSON, err := marshalJSON(ObjectCreatedPayload{Kind: kind, Object: objectJSON})
	if err != nil {
		return err
	}
	p.created = append(p.created, Event{Kind: EventObjectCreated, ObjectKind: kind, ObjectID: id, RevisionID: p.revision, At: p.now, Payload: payloadJSON})
	return nil
}

func (p *commitPlanner) producer() Producer { return p.request.Producer }

func (p *commitPlanner) planObservations() error {
	for _, in := range p.request.Delta.Observations {
		if err := p.checkRef(ObjectObservation, in.LocalRef); err != nil {
			return err
		}
		if in.DerivedFrom != "" && (!p.producer().Deterministic || p.producer().Transform == "" || p.producer().Version == "") {
			return errors.New("derived observation requires deterministic transform provenance")
		}
		id, err := p.mint(in.LocalRef, ObjectObservation)
		if err != nil {
			return err
		}
		if err := p.appendCreated(ObjectObservation, id, Observation{ID: id, Content: in.Content, ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now}); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planClaims() error {
	for _, in := range p.request.Delta.Claims {
		if err := p.checkRef(ObjectClaim, in.LocalRef); err != nil {
			return err
		}
		if strings.TrimSpace(in.Text) == "" {
			return errors.New("claim text is empty")
		}
		id, err := p.mint(in.LocalRef, ObjectClaim)
		if err != nil {
			return err
		}
		if err := p.appendCreated(ObjectClaim, id, Claim{ID: id, Text: in.Text, ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now}); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planHypotheses() error {
	for _, in := range p.request.Delta.Hypotheses {
		if err := p.checkRef(ObjectHypothesis, in.LocalRef); err != nil {
			return err
		}
		if strings.TrimSpace(in.Mechanism) == "" {
			return errors.New("hypothesis mechanism is empty")
		}
		id, err := p.mint(in.LocalRef, ObjectHypothesis)
		if err != nil {
			return err
		}
		value := Hypothesis{ID: id, Mechanism: in.Mechanism, Falsifier: in.Falsifier, Status: HypothesisProposed, ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now}
		if err := p.appendCreated(ObjectHypothesis, id, value); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planUnknowns() error {
	for _, in := range p.request.Delta.Unknowns {
		if err := p.checkRef(ObjectUnknown, in.LocalRef); err != nil {
			return err
		}
		if strings.TrimSpace(in.Question) == "" {
			return errors.New("unknown question is empty")
		}
		id, err := p.mint(in.LocalRef, ObjectUnknown)
		if err != nil {
			return err
		}
		value := Unknown{ID: id, Question: in.Question, Status: UnknownOpen, ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now}
		if err := p.appendCreated(ObjectUnknown, id, value); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planConstraints() error {
	for _, in := range p.request.Delta.Constraints {
		if err := p.checkRef(ObjectConstraint, in.LocalRef); err != nil {
			return err
		}
		if strings.TrimSpace(in.Text) == "" {
			return errors.New("constraint text is empty")
		}
		id, err := p.mint(in.LocalRef, ObjectConstraint)
		if err != nil {
			return err
		}
		if err := p.appendCreated(ObjectConstraint, id, Constraint{ID: id, Text: in.Text, ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now}); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planFrames() error {
	for _, in := range p.request.Delta.Frames {
		if err := p.checkRef(ObjectFrame, in.LocalRef); err != nil {
			return err
		}
		if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Summary) == "" {
			return errors.New("frame name and summary are required")
		}
		id, err := p.mint(in.LocalRef, ObjectFrame)
		if err != nil {
			return err
		}
		value := Frame{
			ID: id, Name: in.Name, Summary: in.Summary,
			CompletionConditions: append([]string(nil), in.CompletionConditions...), DisconfirmationConditions: append([]string(nil), in.DisconfirmationConditions...),
			Status: FramePending, ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now,
		}
		if err := p.appendCreated(ObjectFrame, id, value); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planActions() error {
	for _, in := range p.request.Delta.Actions {
		if err := p.checkRef(ObjectAction, in.LocalRef); err != nil {
			return err
		}
		if strings.TrimSpace(in.Description) == "" {
			return errors.New("action description is empty")
		}
		id, err := p.mint(in.LocalRef, ObjectAction)
		if err != nil {
			return err
		}
		value := Action{ID: id, Description: in.Description, Status: "recorded", ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now}
		if err := p.appendCreated(ObjectAction, id, value); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planOutcomes() error {
	for _, in := range p.request.Delta.Outcomes {
		if err := p.checkRef(ObjectOutcome, in.LocalRef); err != nil {
			return err
		}
		if strings.TrimSpace(in.Description) == "" {
			return errors.New("outcome description is empty")
		}
		id, err := p.mint(in.LocalRef, ObjectOutcome)
		if err != nil {
			return err
		}
		if err := p.appendCreated(ObjectOutcome, id, Outcome{ID: id, Description: in.Description, ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now}); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) resolve(ref string) (ID, ObjectKind, error) {
	if id, ok := p.localIDs[ref]; ok {
		return id, p.localKinds[ref], nil
	}
	id := ID(ref)
	kind, ok := objectKind(p.store.state, id)
	if !ok {
		return "", "", fmt.Errorf("reference %q does not exist", ref)
	}
	if !projected(p.request.Projection, id) {
		return "", "", fmt.Errorf("reference %q is outside the phase projection", ref)
	}
	return id, kind, nil
}

func (p *commitPlanner) allKinds(id ID) (ObjectKind, bool) {
	if kind, ok := p.localObjects[id]; ok {
		return kind, true
	}
	return objectKind(p.store.state, id)
}

func (p *commitPlanner) addRelation(ref string, kind RelationKind, sourceRef, targetRef string) error {
	if strings.TrimSpace(ref) == "" || strings.HasPrefix(ref, "ces_") {
		return fmt.Errorf("invalid relation local ref %q", ref)
	}
	if _, exists := p.localKinds[ref]; exists {
		return fmt.Errorf("duplicate local ref %q", ref)
	}
	if !canAssertRelation(p.request.Phase, kind) {
		return fmt.Errorf("phase %q cannot assert relation %q", p.request.Phase, kind)
	}
	source, sourceKind, err := p.resolve(sourceRef)
	if err != nil {
		return err
	}
	target, targetKind, err := p.resolve(targetRef)
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
	relation := Relation{ID: id, Kind: kind, SourceID: source, TargetID: target, Status: RelationActive, ProducedBy: p.producer(), RevisionID: p.revision, CreatedAt: p.now}
	p.localKinds[ref], p.localIDs[ref] = "relation", id
	p.result.IDs[ref] = id
	p.relations = append(p.relations, relation)
	relationJSON, err := marshalJSON(relation)
	if err != nil {
		return err
	}
	p.relationEvents = append(p.relationEvents, Event{Kind: EventRelationCreated, ObjectKind: "relation", ObjectID: id, RevisionID: p.revision, At: p.now, Payload: relationJSON})
	return nil
}

func (p *commitPlanner) planRelations() error {
	for _, in := range p.request.Delta.Observations {
		if in.DerivedFrom == "" {
			continue
		}
		if _, sourceKind, err := p.resolve(in.DerivedFrom); err != nil {
			return err
		} else if sourceKind != ObjectObservation {
			return errors.New("derived observation source must be an observation")
		}
		if err := p.addRelation("derived_"+in.LocalRef, RelationDerivedFrom, in.LocalRef, in.DerivedFrom); err != nil {
			return err
		}
	}
	for _, in := range p.request.Delta.Relations {
		if err := p.addRelation(in.LocalRef, in.Kind, in.Source, in.Target); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planClaimSupport() error {
	for _, claim := range p.request.Delta.Claims {
		claimID := p.localIDs[claim.LocalRef]
		supported := false
		for _, relation := range p.relations {
			if relation.Kind == RelationSupports && relation.TargetID == claimID {
				sourceKind, _ := p.allKinds(relation.SourceID)
				supported = sourceKind == ObjectObservation || sourceKind == ObjectClaim
			}
		}
		for _, relation := range p.store.state.Relations {
			if relation.Status == RelationActive && relation.Kind == RelationSupports && relation.TargetID == claimID {
				supported = true
			}
		}
		if !supported {
			return fmt.Errorf("claim %q has no supporting source", claim.LocalRef)
		}
	}
	return nil
}

func (p *commitPlanner) planLeadingHypothesis() error {
	if p.request.Delta.LeadingHypothesis == "" {
		return nil
	}
	if p.request.Phase != PhaseAbduce {
		return errors.New("only abduction may select a leading hypothesis")
	}
	id, kind, err := p.resolve(p.request.Delta.LeadingHypothesis)
	if err != nil {
		return err
	}
	if kind != ObjectHypothesis {
		return errors.New("leading reference is not a hypothesis")
	}
	if p.store.state.Task.LeadingHypothesis != "" && p.store.state.Task.LeadingHypothesis != id {
		if err := p.store.prepareStatusEvent(&p.created, p.revision, p.now, ObjectHypothesis, p.store.state.Task.LeadingHypothesis, string(HypothesisLive)); err != nil {
			return err
		}
	}
	return p.store.prepareStatusEvent(&p.created, p.revision, p.now, ObjectHypothesis, id, string(HypothesisLeading))
}

func (p *commitPlanner) planActiveFrame() error {
	if p.request.Delta.ActiveFrame == "" {
		if len(p.request.Delta.Frames) > 0 {
			return errors.New("new frame requires an active frame selection")
		}
		return nil
	}
	if p.request.Phase != PhaseFrame {
		return errors.New("only framing may activate a frame")
	}
	id, kind, err := p.resolve(p.request.Delta.ActiveFrame)
	if err != nil {
		return err
	}
	if kind != ObjectFrame || p.localKinds[p.request.Delta.ActiveFrame] != ObjectFrame {
		return errors.New("active frame must be created in this commit")
	}
	if len(p.request.Delta.Frames) != 1 {
		return errors.New("commit must select exactly one new active frame")
	}
	if p.store.state.Task.ActiveFrame != "" {
		found := false
		for _, relation := range p.relations {
			if relation.Kind == RelationSupersedes && relation.SourceID == id && relation.TargetID == p.store.state.Task.ActiveFrame {
				found = true
			}
		}
		if !found {
			return errors.New("new frame must supersede the active frame")
		}
		if err := p.store.prepareStatusEvent(&p.created, p.revision, p.now, ObjectFrame, p.store.state.Task.ActiveFrame, string(FrameSuperseded)); err != nil {
			return err
		}
	}
	return p.store.prepareStatusEvent(&p.created, p.revision, p.now, ObjectFrame, id, string(FrameActive))
}

func (p *commitPlanner) planStatusChanges() error {
	for _, change := range p.request.Delta.StatusChanges {
		id, kind, err := p.resolve(change.Ref)
		if err != nil {
			return err
		}
		if kind != change.Kind {
			return fmt.Errorf("status reference %q is %s, not %s", change.Ref, kind, change.Kind)
		}
		if err := validateStatusChange(p.request.Phase, p.store.state, kind, id, change.Status); err != nil {
			return err
		}
		if kind == ObjectObservation {
			return errors.New("observations are immutable")
		}
		if kind != ObjectHypothesis && kind != ObjectUnknown && kind != ObjectFrame {
			return fmt.Errorf("status changes are not supported for %s", kind)
		}
		if err := p.store.prepareStatusEvent(&p.created, p.revision, p.now, kind, id, change.Status); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planResolvedUnknowns() error {
	for _, relation := range p.relations {
		if relation.Kind != RelationResolves {
			continue
		}
		if err := p.store.prepareStatusEvent(&p.created, p.revision, p.now, ObjectUnknown, relation.TargetID, string(UnknownResolved)); err != nil {
			return err
		}
	}
	return nil
}

func (p *commitPlanner) planRetractions() error {
	for _, input := range p.request.Delta.Retractions {
		id, kind, err := p.resolve(input.Relation)
		if err != nil {
			return err
		}
		if kind != "relation" {
			return errors.New("retraction target is not a relation")
		}
		var relation *Relation
		for i := range p.store.state.Relations {
			if p.store.state.Relations[i].ID == id {
				relation = &p.store.state.Relations[i]
				break
			}
		}
		if relation == nil || relation.Status != RelationActive {
			return errors.New("relation is not active")
		}
		if !canAssertRelation(p.request.Phase, relation.Kind) {
			return fmt.Errorf("phase %q cannot retract relation %q", p.request.Phase, relation.Kind)
		}
		var reason ID
		if input.ReasonRef != "" {
			reason, kind, err = p.resolve(input.ReasonRef)
			if err != nil {
				return err
			}
			if kind != ObjectClaim {
				return errors.New("retraction reason must be a projected claim")
			}
		}
		if relation.Kind == RelationContradicts && reason == "" {
			return errors.New("contradiction retraction requires a projected reason claim")
		}
		retractionJSON, err := marshalJSON(relationStatusPayload{ID: id, From: relation.Status, To: RelationRetracted, ReasonRef: reason})
		if err != nil {
			return err
		}
		p.retractions = append(p.retractions, Event{Kind: EventRelationStatusChanged, ObjectKind: "relation", ObjectID: id, RevisionID: p.revision, At: p.now, Payload: retractionJSON})
	}
	return nil
}
