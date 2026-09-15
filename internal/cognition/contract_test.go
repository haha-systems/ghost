package cognition

import (
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/epistemic"
)

// Every phase gets a contract that overrides "finish the task", names the
// authority boundary, and carries the schema the strict parser accepts.
func TestEveryPhaseHasAContractWithABoundaryAndASchema(t *testing.T) {
	for _, phase := range []epistemic.Phase{epistemic.PhaseTriage, epistemic.PhaseAbduce, epistemic.PhaseFrame, epistemic.PhaseExecute, epistemic.PhaseClose} {
		contract, ok := Contract(phase)
		if !ok {
			t.Fatalf("no contract for %s", phase)
		}
		text := contract.String()
		for _, want := range []string{
			"CES PHASE: " + strings.ToUpper(string(phase)),
			"overrides the normal instruction to complete the",
			"Ghost will decide what happens next",
			"PURPOSE", "YOU MAY", "YOU MUST NOT", "OUTPUT", "qac_request",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s contract is missing %q", phase, want)
			}
		}
		if len(contract.May) == 0 || len(contract.MustNot) == 0 {
			t.Fatalf("%s contract has no authority boundary", phase)
		}
	}
}

// Each phase must refuse the work that belongs to a later phase.
func TestPhaseContractsProhibitLaterPhaseWork(t *testing.T) {
	cases := map[epistemic.Phase][]string{
		epistemic.PhaseTriage:  {"root cause", "fix", "production file", "complete"},
		epistemic.PhaseAbduce:  {"Implement", "edit any production file", "complete"},
		epistemic.PhaseFrame:   {"production file", "complete"},
		epistemic.PhaseExecute: {"outside the active frame", "reframe", "verified"},
		epistemic.PhaseClose:   {"Repair", "Suppress", "work status"},
	}
	for phase, wants := range cases {
		contract, _ := Contract(phase)
		prohibited := strings.Join(contract.MustNot, "\n")
		for _, want := range wants {
			if !strings.Contains(prohibited, want) {
				t.Fatalf("%s contract does not prohibit %q: %s", phase, want, prohibited)
			}
		}
	}
}

// The contract's output template must be the shape the strict parser accepts,
// not a description of one. A field the parser rejects would fail every run.
func TestContractOutputNamesOnlyParsedFields(t *testing.T) {
	fields := map[epistemic.Phase][]string{
		epistemic.PhaseTriage:  {"classification", "boundaries", "observations", "claims", "unknowns", "resolved_unknowns", "next_investigation", "qac_request"},
		epistemic.PhaseAbduce:  {"hypotheses", "relations", "leading_hypothesis_ref", "remaining_uncertainty", "resolved_unknowns", "qac_request"},
		epistemic.PhaseFrame:   {"frame", "constraints", "relations", "supersedes_frame_ref", "qac_request"},
		epistemic.PhaseExecute: {"observations", "actions", "outcomes", "observation_refs", "relations", "resolved_unknowns", "qac_request"},
		epistemic.PhaseClose:   {"observations", "verification_observation_refs", "relations", "residual_uncertainty", "completion_recommended", "qac_request"},
	}
	for phase, wants := range fields {
		contract, _ := Contract(phase)
		for _, want := range wants {
			if !strings.Contains(contract.Output, `"`+want+`"`) {
				t.Fatalf("%s output template is missing %q", phase, want)
			}
		}
	}
}
