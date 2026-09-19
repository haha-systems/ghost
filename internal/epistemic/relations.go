package epistemic

// RelationEndpoint describes one legal source/target object-kind pair.
type RelationEndpoint struct {
	Source ObjectKind
	Target ObjectKind
}

// RelationSpec is the canonical CES relation algebra. Phase permissions and
// endpoint validation must both be derived from this specification.
type RelationSpec struct {
	Kind          RelationKind
	AllowedPhases []Phase
	Endpoints     []RelationEndpoint
}

var relationSpecs = []RelationSpec{
	{Kind: RelationSupports, AllowedPhases: []Phase{PhaseTriage, PhaseAbduce}, Endpoints: relationEndpoints(
		[]ObjectKind{ObjectObservation, ObjectClaim},
		[]ObjectKind{ObjectClaim, ObjectHypothesis, ObjectFrame, ObjectAction, ObjectOutcome},
	)},
	{Kind: RelationContradicts, AllowedPhases: []Phase{PhaseAbduce, PhaseExecute, PhaseClose}, Endpoints: relationEndpoints(
		[]ObjectKind{ObjectObservation, ObjectClaim},
		[]ObjectKind{ObjectClaim, ObjectHypothesis, ObjectFrame, ObjectAction, ObjectOutcome},
	)},
	{Kind: RelationDerivedFrom, AllowedPhases: []Phase{PhaseTriage, PhaseAbduce, PhaseFrame, PhaseExecute, PhaseClose}, Endpoints: relationEndpoints(
		[]ObjectKind{ObjectObservation}, []ObjectKind{ObjectObservation},
	)},
	{Kind: RelationDependsOn, AllowedPhases: []Phase{PhaseFrame}, Endpoints: relationEndpoints(
		[]ObjectKind{ObjectFrame}, []ObjectKind{ObjectHypothesis, ObjectConstraint, ObjectFrame},
	)},
	{Kind: RelationImplements, AllowedPhases: []Phase{PhaseExecute, PhaseClose}, Endpoints: relationEndpoints(
		[]ObjectKind{ObjectAction}, []ObjectKind{ObjectFrame, ObjectConstraint, ObjectHypothesis},
	)},
	{Kind: RelationTests, AllowedPhases: []Phase{PhaseExecute, PhaseClose}, Endpoints: relationEndpoints(
		[]ObjectKind{ObjectObservation, ObjectClaim, ObjectOutcome},
		[]ObjectKind{ObjectAction, ObjectOutcome, ObjectHypothesis, ObjectFrame},
	)},
	{Kind: RelationResolves, AllowedPhases: []Phase{PhaseTriage, PhaseAbduce, PhaseFrame, PhaseExecute, PhaseClose}, Endpoints: relationEndpoints(
		[]ObjectKind{ObjectObservation, ObjectClaim}, []ObjectKind{ObjectUnknown},
	)},
	{Kind: RelationSupersedes, AllowedPhases: []Phase{PhaseFrame}, Endpoints: []RelationEndpoint{
		{Source: ObjectFrame, Target: ObjectFrame},
		{Source: ObjectHypothesis, Target: ObjectHypothesis},
	}},
}

func relationEndpoints(sources, targets []ObjectKind) []RelationEndpoint {
	result := make([]RelationEndpoint, 0, len(sources)*len(targets))
	for _, source := range sources {
		for _, target := range targets {
			result = append(result, RelationEndpoint{Source: source, Target: target})
		}
	}
	return result
}

// RelationSpecifications returns a copy of the canonical relation algebra.
func RelationSpecifications() []RelationSpec {
	result := make([]RelationSpec, len(relationSpecs))
	for i, spec := range relationSpecs {
		result[i] = RelationSpec{
			Kind:          spec.Kind,
			AllowedPhases: append([]Phase(nil), spec.AllowedPhases...),
			Endpoints:     append([]RelationEndpoint(nil), spec.Endpoints...),
		}
	}
	return result
}

// CanAssertRelation reports whether a phase may create or retract a relation.
func CanAssertRelation(phase Phase, kind RelationKind) bool {
	spec, ok := relationSpec(kind)
	if !ok {
		return false
	}
	for _, allowed := range spec.AllowedPhases {
		if allowed == phase {
			return true
		}
	}
	return false
}

// ValidRelationEndpoints reports whether a relation connects legal object kinds.
func ValidRelationEndpoints(kind RelationKind, source, target ObjectKind) bool {
	spec, ok := relationSpec(kind)
	if !ok {
		return false
	}
	for _, endpoint := range spec.Endpoints {
		if endpoint.Source == source && endpoint.Target == target {
			return true
		}
	}
	return false
}

func relationSpec(kind RelationKind) (RelationSpec, bool) {
	for _, spec := range relationSpecs {
		if spec.Kind == kind {
			return spec, true
		}
	}
	return RelationSpec{}, false
}
