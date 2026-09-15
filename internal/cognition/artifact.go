package cognition

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/haha-systems/ghost/internal/epistemic"
)

type ArtifactRelation struct {
	LocalRef string                 `json:"local_ref"`
	Kind     epistemic.RelationKind `json:"kind"`
	Source   string                 `json:"source"`
	Target   string                 `json:"target"`
}

// ArtifactObservation is raw evidence an agent recorded during a phase. The
// store accepts observations in triage, execution, and closure; without them a
// claim or a contradiction has nothing to rest on.
type ArtifactObservation struct {
	LocalRef string `json:"local_ref"`
	Content  string `json:"content"`
}

type ArtifactClaim struct {
	LocalRef    string `json:"local_ref"`
	Text        string `json:"text"`
	EvidenceRef string `json:"evidence_ref"`
}

type ArtifactUnknown struct {
	LocalRef string `json:"local_ref"`
	Question string `json:"question"`
}

type ArtifactHypothesis struct {
	LocalRef  string `json:"local_ref"`
	Mechanism string `json:"mechanism"`
	Falsifier string `json:"falsifier"`
}

type ArtifactConstraint struct {
	LocalRef string `json:"local_ref"`
	Text     string `json:"text"`
}

type ArtifactFrame struct {
	LocalRef                  string   `json:"local_ref"`
	Name                      string   `json:"name"`
	Summary                   string   `json:"summary"`
	CompletionConditions      []string `json:"completion_conditions"`
	DisconfirmationConditions []string `json:"disconfirmation_conditions"`
}

type ArtifactAction struct {
	LocalRef    string `json:"local_ref"`
	Description string `json:"description"`
}

type ArtifactOutcome struct {
	LocalRef    string `json:"local_ref"`
	Description string `json:"description"`
}

type TriageArtifact struct {
	Classification    string                `json:"classification"`
	Boundaries        []string              `json:"boundaries"`
	Observations      []ArtifactObservation `json:"observations"`
	Claims            []ArtifactClaim       `json:"claims"`
	Unknowns          []ArtifactUnknown     `json:"unknowns"`
	ResolvedUnknowns  []ArtifactRelation    `json:"resolved_unknowns"`
	NextInvestigation string                `json:"next_investigation"`
	ResourceRequest   QACRequest            `json:"qac_request"`
}

type AbductionArtifact struct {
	Hypotheses           []ArtifactHypothesis `json:"hypotheses"`
	Relations            []ArtifactRelation   `json:"relations"`
	LeadingHypothesisRef string               `json:"leading_hypothesis_ref"`
	RemainingUncertainty string               `json:"remaining_uncertainty"`
	ResolvedUnknowns     []ArtifactRelation   `json:"resolved_unknowns"`
	ResourceRequest      QACRequest           `json:"qac_request"`
}

type FrameArtifact struct {
	Frame              ArtifactFrame        `json:"frame"`
	Constraints        []ArtifactConstraint `json:"constraints"`
	Relations          []ArtifactRelation   `json:"relations"`
	SupersedesFrameRef string               `json:"supersedes_frame_ref"`
	ResourceRequest    QACRequest           `json:"qac_request"`
}

type ExecutionArtifact struct {
	Observations     []ArtifactObservation `json:"observations"`
	Actions          []ArtifactAction      `json:"actions"`
	Outcomes         []ArtifactOutcome     `json:"outcomes"`
	ObservationRefs  []string              `json:"observation_refs"`
	Relations        []ArtifactRelation    `json:"relations"`
	ResolvedUnknowns []ArtifactRelation    `json:"resolved_unknowns"`
	ResourceRequest  QACRequest            `json:"qac_request"`
}

type ClosureArtifact struct {
	Observations                []ArtifactObservation `json:"observations"`
	VerificationObservationRefs []string              `json:"verification_observation_refs"`
	Relations                   []ArtifactRelation    `json:"relations"`
	ResidualUncertainty         string                `json:"residual_uncertainty"`
	CompletionRecommended       bool                  `json:"completion_recommended"`
	ResourceRequest             QACRequest            `json:"qac_request"`
}

type PhaseArtifact struct {
	Phase epistemic.Phase
	value any
}

func ParsePhaseArtifact(phase epistemic.Phase, data []byte) (PhaseArtifact, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return PhaseArtifact{}, errors.New("phase artifact is empty")
	}
	switch phase {
	case epistemic.PhaseTriage:
		var value TriageArtifact
		if err := decodeStrict(data, &value); err != nil {
			return PhaseArtifact{}, err
		}
		if strings.TrimSpace(value.Classification) == "" || strings.TrimSpace(value.NextInvestigation) == "" {
			return PhaseArtifact{}, errors.New("triage needs a classification and next investigation")
		}
		return PhaseArtifact{Phase: phase, value: value}, nil
	case epistemic.PhaseAbduce:
		var value AbductionArtifact
		if err := decodeStrict(data, &value); err != nil {
			return PhaseArtifact{}, err
		}
		if len(value.Hypotheses) == 0 || strings.TrimSpace(value.LeadingHypothesisRef) == "" {
			return PhaseArtifact{}, errors.New("abduction needs hypotheses and a leading hypothesis")
		}
		return PhaseArtifact{Phase: phase, value: value}, nil
	case epistemic.PhaseFrame:
		var value FrameArtifact
		if err := decodeStrict(data, &value); err != nil {
			return PhaseArtifact{}, err
		}
		if strings.TrimSpace(value.Frame.LocalRef) == "" || strings.TrimSpace(value.Frame.Name) == "" || strings.TrimSpace(value.Frame.Summary) == "" {
			return PhaseArtifact{}, errors.New("frame needs a local ref, name, and summary")
		}
		return PhaseArtifact{Phase: phase, value: value}, nil
	case epistemic.PhaseExecute:
		var value ExecutionArtifact
		if err := decodeStrict(data, &value); err != nil {
			return PhaseArtifact{}, err
		}
		if len(value.Actions)+len(value.Outcomes)+len(value.Relations) == 0 {
			return PhaseArtifact{}, errors.New("execution artifact has no actions, outcomes, or relations")
		}
		return PhaseArtifact{Phase: phase, value: value}, nil
	case epistemic.PhaseClose:
		var value ClosureArtifact
		if err := decodeStrict(data, &value); err != nil {
			return PhaseArtifact{}, err
		}
		if len(value.VerificationObservationRefs) == 0 && len(value.Relations) == 0 && strings.TrimSpace(value.ResidualUncertainty) == "" {
			return PhaseArtifact{}, errors.New("closure artifact has no verification, relations, or residual uncertainty")
		}
		return PhaseArtifact{Phase: phase, value: value}, nil
	default:
		return PhaseArtifact{}, fmt.Errorf("unknown phase %q", phase)
	}
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode phase artifact: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("phase artifact has trailing JSON")
		}
		return fmt.Errorf("read phase artifact: %w", err)
	}
	return nil
}

func (artifact PhaseArtifact) Delta() (epistemic.Delta, error) {
	if artifact.Phase == "" || artifact.value == nil {
		return epistemic.Delta{}, errors.New("phase artifact is empty")
	}
	var delta epistemic.Delta
	switch value := artifact.value.(type) {
	case TriageArtifact:
		delta.Observations = appendArtifactObservations(delta.Observations, value.Observations)
		for _, claim := range value.Claims {
			delta.Claims = append(delta.Claims, epistemic.ClaimInput{LocalRef: claim.LocalRef, Text: claim.Text})
			// A claim with no cited evidence is still a claim. Emitting a
			// supports relation from an empty reference would make the whole
			// artifact uncommittable instead.
			if strings.TrimSpace(claim.EvidenceRef) != "" {
				delta.Relations = append(delta.Relations, epistemic.RelationInput{LocalRef: "supports_" + claim.LocalRef, Kind: epistemic.RelationSupports, Source: claim.EvidenceRef, Target: claim.LocalRef})
			}
		}
		for _, unknown := range value.Unknowns {
			delta.Unknowns = append(delta.Unknowns, epistemic.UnknownInput{LocalRef: unknown.LocalRef, Question: unknown.Question})
		}
		delta.Relations = appendArtifactRelations(delta.Relations, value.ResolvedUnknowns)
	case AbductionArtifact:
		for _, hypothesis := range value.Hypotheses {
			delta.Hypotheses = append(delta.Hypotheses, epistemic.HypothesisInput{LocalRef: hypothesis.LocalRef, Mechanism: hypothesis.Mechanism, Falsifier: hypothesis.Falsifier})
		}
		delta.Relations = appendArtifactRelations(delta.Relations, value.Relations)
		delta.Relations = appendArtifactRelations(delta.Relations, value.ResolvedUnknowns)
		delta.LeadingHypothesis = value.LeadingHypothesisRef
	case FrameArtifact:
		delta.Frames = append(delta.Frames, epistemic.FrameInput{LocalRef: value.Frame.LocalRef, Name: value.Frame.Name, Summary: value.Frame.Summary, CompletionConditions: value.Frame.CompletionConditions, DisconfirmationConditions: value.Frame.DisconfirmationConditions})
		for _, constraint := range value.Constraints {
			delta.Constraints = append(delta.Constraints, epistemic.ConstraintInput{LocalRef: constraint.LocalRef, Text: constraint.Text})
		}
		delta.Relations = appendArtifactRelations(delta.Relations, value.Relations)
		if value.SupersedesFrameRef != "" {
			delta.Relations = append(delta.Relations, epistemic.RelationInput{LocalRef: "supersedes_" + value.Frame.LocalRef, Kind: epistemic.RelationSupersedes, Source: value.Frame.LocalRef, Target: value.SupersedesFrameRef})
		}
		delta.ActiveFrame = value.Frame.LocalRef
	case ExecutionArtifact:
		delta.Observations = appendArtifactObservations(delta.Observations, value.Observations)
		for _, action := range value.Actions {
			delta.Actions = append(delta.Actions, epistemic.ActionInput{LocalRef: action.LocalRef, Description: action.Description})
		}
		for _, outcome := range value.Outcomes {
			delta.Outcomes = append(delta.Outcomes, epistemic.OutcomeInput{LocalRef: outcome.LocalRef, Description: outcome.Description})
		}
		delta.Relations = appendArtifactRelations(delta.Relations, value.Relations)
		delta.Relations = appendArtifactRelations(delta.Relations, value.ResolvedUnknowns)
	case ClosureArtifact:
		delta.Observations = appendArtifactObservations(delta.Observations, value.Observations)
		delta.Relations = appendArtifactRelations(delta.Relations, value.Relations)
	default:
		return epistemic.Delta{}, fmt.Errorf("artifact value %T does not match phase %q", artifact.value, artifact.Phase)
	}
	return delta, nil
}

func appendArtifactObservations(destination []epistemic.ObservationInput, observations []ArtifactObservation) []epistemic.ObservationInput {
	for _, observation := range observations {
		destination = append(destination, epistemic.ObservationInput{LocalRef: observation.LocalRef, Content: observation.Content})
	}
	return destination
}

func appendArtifactRelations(destination []epistemic.RelationInput, relations []ArtifactRelation) []epistemic.RelationInput {
	for i, relation := range relations {
		ref := relation.LocalRef
		if ref == "" {
			ref = fmt.Sprintf("relation_%d", len(destination)+i+1)
		}
		destination = append(destination, epistemic.RelationInput{LocalRef: ref, Kind: relation.Kind, Source: relation.Source, Target: relation.Target})
	}
	return destination
}

func (artifact PhaseArtifact) QACRequest() QACRequest {
	switch value := artifact.value.(type) {
	case TriageArtifact:
		return value.ResourceRequest
	case AbductionArtifact:
		return value.ResourceRequest
	case FrameArtifact:
		return value.ResourceRequest
	case ExecutionArtifact:
		return value.ResourceRequest
	case ClosureArtifact:
		return value.ResourceRequest
	default:
		return QACRequest{}
	}
}

// CompletionRecommended reports the closure artifact's recommendation. It is
// evidence for the orchestrator, not a decision: only CES can complete work.
func (artifact PhaseArtifact) CompletionRecommended() bool {
	value, ok := artifact.value.(ClosureArtifact)
	return ok && value.CompletionRecommended
}
