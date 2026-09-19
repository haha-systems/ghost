package cognition

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/epistemic"
)

// The contract must not advertise a relation shape that CES cannot accept.
// This test reads the model-facing schema and checks its declared endpoint
// shapes against the canonical CES relation algebra.
func TestPhaseContractsAdvertiseOnlyCommitableRelationShapes(t *testing.T) {
	kindPattern := regexp.MustCompile(`"kind": ([^,}]+)`)
	for _, phase := range []epistemic.Phase{epistemic.PhaseTriage, epistemic.PhaseAbduce, epistemic.PhaseFrame, epistemic.PhaseExecute, epistemic.PhaseClose} {
		contract, ok := Contract(phase)
		if !ok {
			t.Fatalf("no contract for %s", phase)
		}
		advertised := map[epistemic.RelationKind]bool{}
		for _, match := range kindPattern.FindAllStringSubmatch(contract.Output, -1) {
			for _, value := range strings.Split(match[1], "|") {
				value = strings.Trim(strings.TrimSpace(value), `"`)
				if value != "" {
					advertised[epistemic.RelationKind(value)] = true
				}
			}
		}
		declared := map[epistemic.RelationKind]bool{}
		explicit := map[epistemic.RelationKind]bool{}
		for _, relation := range contract.Relations {
			if declared[relation.Kind] {
				t.Errorf("%s declares relation %q more than once", phase, relation.Kind)
			}
			declared[relation.Kind] = true
			if !relation.Implicit {
				explicit[relation.Kind] = true
			}
			if !epistemic.CanAssertRelation(phase, relation.Kind) {
				t.Errorf("%s cannot assert %q", phase, relation.Kind)
			}
			if len(relation.SourceKinds) == 0 || len(relation.TargetKinds) == 0 {
				t.Errorf("%s relation %q has incomplete endpoint declaration", phase, relation.Kind)
			}
			for _, source := range relation.SourceKinds {
				for _, target := range relation.TargetKinds {
					if !epistemic.ValidRelationEndpoints(relation.Kind, source, target) {
						t.Errorf("%s contract shape %q: %s -> %s is not canonical", phase, relation.Kind, source, target)
					}
				}
			}
		}
		if !sameRelationKinds(advertised, explicit) {
			t.Fatalf("%s advertises %v, explicitly declares %v", phase, relationKindNames(advertised), relationKindNames(explicit))
		}
	}
}

func sameRelationKinds(left, right map[epistemic.RelationKind]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for kind := range left {
		if !right[kind] {
			return false
		}
	}
	return true
}

func relationKindNames(kinds map[epistemic.RelationKind]bool) []string {
	result := make([]string, 0, len(kinds))
	for kind := range kinds {
		result = append(result, string(kind))
	}
	sort.Strings(result)
	return result
}
