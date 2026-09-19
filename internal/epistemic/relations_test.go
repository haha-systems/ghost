package epistemic

import "testing"

func TestRelationSpecificationsAcceptEveryDeclaredShape(t *testing.T) {
	for _, spec := range RelationSpecifications() {
		if len(spec.AllowedPhases) == 0 || len(spec.Endpoints) == 0 {
			t.Fatalf("relation %q has incomplete specification", spec.Kind)
		}
		for _, phase := range spec.AllowedPhases {
			if !CanAssertRelation(phase, spec.Kind) {
				t.Errorf("relation %q is not assertable in declared phase %q", spec.Kind, phase)
			}
		}
		for _, endpoint := range spec.Endpoints {
			if !ValidRelationEndpoints(spec.Kind, endpoint.Source, endpoint.Target) {
				t.Errorf("relation %q rejects declared shape %s -> %s", spec.Kind, endpoint.Source, endpoint.Target)
			}
		}
	}
}

func TestRelationSpecificationsRejectIllegalShapesAndAuthorities(t *testing.T) {
	illegalShapes := []struct {
		kind           RelationKind
		source, target ObjectKind
	}{
		{RelationDerivedFrom, ObjectFrame, ObjectHypothesis},
		{RelationDependsOn, ObjectHypothesis, ObjectUnknown},
		{RelationImplements, ObjectAction, ObjectOutcome},
		{RelationResolves, ObjectHypothesis, ObjectUnknown},
		{RelationTests, ObjectAction, ObjectOutcome},
		{RelationSupersedes, ObjectFrame, ObjectHypothesis},
	}
	for _, test := range illegalShapes {
		if ValidRelationEndpoints(test.kind, test.source, test.target) {
			t.Errorf("relation %q accepts illegal shape %s -> %s", test.kind, test.source, test.target)
		}
	}

	illegalAuthorities := []struct {
		phase Phase
		kind  RelationKind
	}{
		{PhaseExecute, RelationSupports},
		{PhaseClose, RelationSupports},
		{PhaseAbduce, RelationDependsOn},
		{PhaseFrame, RelationImplements},
	}
	for _, test := range illegalAuthorities {
		if CanAssertRelation(test.phase, test.kind) {
			t.Errorf("phase %q can assert unauthorized relation %q", test.phase, test.kind)
		}
	}
}
