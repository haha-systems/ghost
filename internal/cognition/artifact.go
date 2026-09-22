package cognition

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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

// ArtifactClaim is a statement an agent asserts to be true, with optional
// supporting evidence. The store accepts claims in triage; without them a
// hypothesis has nothing to explain.
type ArtifactClaim struct {
	LocalRef    string `json:"local_ref"`
	Text        string `json:"text"`
	EvidenceRef string `json:"evidence_ref"`
}

// ArtifactUnknown is a question an agent cannot answer, with optional
// supporting evidence. The store accepts unknowns in triage; without them a
// hypothesis has nothing to falsify.
type ArtifactUnknown struct {
	LocalRef string `json:"local_ref"`
	Question string `json:"question"`
}

// ArtifactHypothesis is a proposed explanation for the observations, claims, and unknowns.
// The store accepts hypotheses in abduction; without them a frame has nothing to constrain.
type ArtifactHypothesis struct {
	LocalRef  string `json:"local_ref"`
	Mechanism string `json:"mechanism"`
	Falsifier string `json:"falsifier"`
}

// ArtifactConstraint is a rule that limits the possible explanations for the observations, claims, and unknowns.
// The store accepts constraints in frame; without them a frame has nothing to enforce.
type ArtifactConstraint struct {
	LocalRef string `json:"local_ref"`
	Text     string `json:"text"`
}

// ArtifactFrame is a summary of the current state of the investigation, including the active frame, its name, and its summary.
// The store accepts frames in frame; without them a frame has nothing to describe.
type ArtifactFrame struct {
	LocalRef                  string   `json:"local_ref"`
	Name                      string   `json:"name"`
	Summary                   string   `json:"summary"`
	CompletionConditions      []string `json:"completion_conditions"`
	DisconfirmationConditions []string `json:"disconfirmation_conditions"`
}

// ArtifactAction is a record of an action taken during the execution phase, with a description of the action.
// The store accepts actions in execution; without them an outcome has nothing to result from.
type ArtifactAction struct {
	LocalRef    string `json:"local_ref"`
	Description string `json:"description"`
}

// ArtifactOutcome is a record of an outcome observed during the execution phase, with a description of the outcome.
// The store accepts outcomes in execution; without them an action has nothing to lead to.
type ArtifactOutcome struct {
	LocalRef    string `json:"local_ref"`
	Description string `json:"description"`
}

// TriageArtifact is the data structure for the triage phase, including classification,
// boundaries, observations, claims, unknowns, resolved unknowns, next investigation, and resource request.
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

// AbductionArtifact is the data structure for the abduction phase, including hypotheses,
// relations, leading hypothesis reference, remaining uncertainty, and resource request.
type AbductionArtifact struct {
	Hypotheses           []ArtifactHypothesis `json:"hypotheses"`
	Relations            []ArtifactRelation   `json:"relations"`
	LeadingHypothesisRef string               `json:"leading_hypothesis_ref"`
	RemainingUncertainty string               `json:"remaining_uncertainty"`
	ResourceRequest      QACRequest           `json:"qac_request"`
}

// FrameArtifact is the data structure for the frame phase, including frame details,
// constraints, relations, supersedes frame reference, and resource request.
type FrameArtifact struct {
	Frame              ArtifactFrame        `json:"frame"`
	Constraints        []ArtifactConstraint `json:"constraints"`
	Relations          []ArtifactRelation   `json:"relations"`
	SupersedesFrameRef string               `json:"supersedes_frame_ref"`
	ResourceRequest    QACRequest           `json:"qac_request"`
}

// ExecutionArtifact is the data structure for the execution phase, including observations,
// actions, outcomes, relations, reported progress, and resource request.
type ExecutionArtifact struct {
	Observations     []ArtifactObservation `json:"observations"`
	Actions          []ArtifactAction      `json:"actions"`
	Outcomes         []ArtifactOutcome     `json:"outcomes"`
	Relations        []ArtifactRelation    `json:"relations"`
	ReportedProgress *ProgressReport       `json:"reported_progress"`
	ResourceRequest  QACRequest            `json:"qac_request"`
}

// ClosureArtifact is the data structure for the closure phase, including observations,
// relations, resolved unknowns, residual uncertainty, and resource request.
type ClosureArtifact struct {
	Observations          []ArtifactObservation `json:"observations"`
	Relations             []ArtifactRelation    `json:"relations"`
	ResolvedUnknowns      []ArtifactRelation    `json:"resolved_unknowns"`
	ResidualUncertainty   string                `json:"residual_uncertainty"`
	CompletionRecommended bool                  `json:"completion_recommended"`
	ResourceRequest       QACRequest            `json:"qac_request"`
}

// PhaseArtifact is a wrapper for the different phase artifacts,
// containing the phase and its corresponding value.
type PhaseArtifact struct {
	Phase epistemic.Phase
	value any
}

// ParsePhaseArtifact parses the given data into a PhaseArtifact based on the specified phase.
// It validates the artifact's content and returns an error if the data is empty or invalid.
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
		if value.ResourceRequest.Direction != "" {
			if err := validateQACRequest(value.ResourceRequest); err != nil {
				return PhaseArtifact{}, err
			}
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
		if value.ResourceRequest.Direction != "" {
			if err := validateQACRequest(value.ResourceRequest); err != nil {
				return PhaseArtifact{}, err
			}
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
		if value.ResourceRequest.Direction != "" {
			if err := validateQACRequest(value.ResourceRequest); err != nil {
				return PhaseArtifact{}, err
			}
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
		if value.ResourceRequest.Direction != "" {
			if err := validateQACRequest(value.ResourceRequest); err != nil {
				return PhaseArtifact{}, err
			}
		}
		if value.ReportedProgress != nil && !validProgressState(value.ReportedProgress.State) {
			return PhaseArtifact{}, fmt.Errorf("invalid reported progress %q", value.ReportedProgress.State)
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
		if value.ResourceRequest.Direction != "" {
			if err := validateQACRequest(value.ResourceRequest); err != nil {
				return PhaseArtifact{}, err
			}
		}
		if len(value.Observations) == 0 && len(value.Relations) == 0 && len(value.ResolvedUnknowns) == 0 && strings.TrimSpace(value.ResidualUncertainty) == "" {
			return PhaseArtifact{}, errors.New("closure artifact has no verification, relations, or residual uncertainty")
		}
		return PhaseArtifact{Phase: phase, value: value}, nil
	default:
		return PhaseArtifact{}, fmt.Errorf("unknown phase %q", phase)
	}
}

func validProgressState(state ProgressState) bool {
	return state == ProgressUnknown || state == ProgressLow || state == ProgressMeaningful
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

// Delta converts the artifact into a delta for the store. It returns an error if
// the artifact is empty or invalid.
//
// The delta contains the observations, claims, hypotheses, unknowns, constraints,
// frames, actions, outcomes, and relations from the artifact.
//
// The delta can be used to update the store with the new information from the artifact.
// The delta does not include the resource request, which is handled separately.
func (artifact PhaseArtifact) Delta() (epistemic.Delta, error) {
	if artifact.Phase == "" || artifact.value == nil {
		return epistemic.Delta{}, errors.New("phase artifact is empty")
	}

	value, ok := artifact.value.(interface {
		delta() epistemic.Delta
	})
	if !ok {
		return epistemic.Delta{}, fmt.Errorf(
			"artifact value %T does not match phase %q",
			artifact.value,
			artifact.Phase,
		)
	}

	return value.delta(), nil
}

func (value TriageArtifact) delta() epistemic.Delta {
	delta := epistemic.Delta{Observations: appendArtifactObservations(nil, value.Observations)}
	for _, claim := range value.Claims {
		delta.Claims = append(delta.Claims, epistemic.ClaimInput{LocalRef: claim.LocalRef, Text: claim.Text})
		// A claim without cited evidence remains a claim, but does not emit an
		// invalid supports relation with an empty source.
		if strings.TrimSpace(claim.EvidenceRef) != "" {
			delta.Relations = append(delta.Relations, epistemic.RelationInput{LocalRef: "supports_" + claim.LocalRef, Kind: epistemic.RelationSupports, Source: claim.EvidenceRef, Target: claim.LocalRef})
		}
	}
	for _, unknown := range value.Unknowns {
		delta.Unknowns = append(delta.Unknowns, epistemic.UnknownInput{LocalRef: unknown.LocalRef, Question: unknown.Question})
	}
	delta.Relations = appendArtifactRelations(delta.Relations, value.ResolvedUnknowns)
	return delta
}

func (value AbductionArtifact) delta() epistemic.Delta {
	delta := epistemic.Delta{LeadingHypothesis: value.LeadingHypothesisRef}
	for _, hypothesis := range value.Hypotheses {
		delta.Hypotheses = append(delta.Hypotheses, epistemic.HypothesisInput{LocalRef: hypothesis.LocalRef, Mechanism: hypothesis.Mechanism, Falsifier: hypothesis.Falsifier})
	}
	delta.Relations = appendArtifactRelations(delta.Relations, value.Relations)
	return delta
}

func (value FrameArtifact) delta() epistemic.Delta {
	delta := epistemic.Delta{
		Frames:      []epistemic.FrameInput{{LocalRef: value.Frame.LocalRef, Name: value.Frame.Name, Summary: value.Frame.Summary, CompletionConditions: value.Frame.CompletionConditions, DisconfirmationConditions: value.Frame.DisconfirmationConditions}},
		ActiveFrame: value.Frame.LocalRef,
	}
	for _, constraint := range value.Constraints {
		delta.Constraints = append(delta.Constraints, epistemic.ConstraintInput{LocalRef: constraint.LocalRef, Text: constraint.Text})
	}
	delta.Relations = appendArtifactRelations(delta.Relations, value.Relations)
	if value.SupersedesFrameRef != "" {
		delta.Relations = append(delta.Relations, epistemic.RelationInput{LocalRef: "supersedes_" + value.Frame.LocalRef, Kind: epistemic.RelationSupersedes, Source: value.Frame.LocalRef, Target: value.SupersedesFrameRef})
	}
	return delta
}

func (value ExecutionArtifact) delta() epistemic.Delta {
	delta := epistemic.Delta{Observations: appendArtifactObservations(nil, value.Observations)}
	for _, action := range value.Actions {
		delta.Actions = append(delta.Actions, epistemic.ActionInput{LocalRef: action.LocalRef, Description: action.Description})
	}
	for _, outcome := range value.Outcomes {
		delta.Outcomes = append(delta.Outcomes, epistemic.OutcomeInput{LocalRef: outcome.LocalRef, Description: outcome.Description})
	}
	delta.Relations = appendArtifactRelations(delta.Relations, value.Relations)
	return delta
}

func (value ClosureArtifact) delta() epistemic.Delta {
	delta := epistemic.Delta{Observations: appendArtifactObservations(nil, value.Observations)}
	delta.Relations = appendArtifactRelations(delta.Relations, value.Relations)
	delta.Relations = appendArtifactRelations(delta.Relations, value.ResolvedUnknowns)
	return delta
}

// Validate resolves every artifact reference against its local objects and the
// phase projection. The store remains authoritative; this catches contract
// violations before the artifact is returned for commit.
func (artifact PhaseArtifact) Validate(projection epistemic.Projection) error {
	if artifact.Phase != projection.Phase {
		return errors.New("phase artifact and projection do not match")
	}
	delta, err := artifact.Delta()
	if err != nil {
		return err
	}
	kinds := epistemic.ObjectKinds(projection.State)
	locals := map[string]epistemic.ObjectKind{}
	register := func(ref string, kind epistemic.ObjectKind) error {
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("%s local ref is empty", kind)
		}
		if _, exists := locals[ref]; exists {
			return fmt.Errorf("duplicate local ref %q", ref)
		}
		locals[ref] = kind
		return nil
	}
	for _, value := range delta.Observations {
		if err := register(value.LocalRef, epistemic.ObjectObservation); err != nil {
			return err
		}
	}
	for _, value := range delta.Claims {
		if err := register(value.LocalRef, epistemic.ObjectClaim); err != nil {
			return err
		}
	}
	for _, value := range delta.Hypotheses {
		if err := register(value.LocalRef, epistemic.ObjectHypothesis); err != nil {
			return err
		}
	}
	for _, value := range delta.Unknowns {
		if err := register(value.LocalRef, epistemic.ObjectUnknown); err != nil {
			return err
		}
	}
	for _, value := range delta.Constraints {
		if err := register(value.LocalRef, epistemic.ObjectConstraint); err != nil {
			return err
		}
	}
	for _, value := range delta.Frames {
		if err := register(value.LocalRef, epistemic.ObjectFrame); err != nil {
			return err
		}
	}
	for _, value := range delta.Actions {
		if err := register(value.LocalRef, epistemic.ObjectAction); err != nil {
			return err
		}
	}
	for _, value := range delta.Outcomes {
		if err := register(value.LocalRef, epistemic.ObjectOutcome); err != nil {
			return err
		}
	}
	for _, relation := range delta.Relations {
		if err := register(relation.LocalRef, epistemic.ObjectKind("relation")); err != nil {
			return err
		}
	}
	resolve := func(ref string) (epistemic.ObjectKind, error) {
		if kind, ok := locals[ref]; ok {
			return kind, nil
		}
		kind, ok := kinds[epistemic.ID(ref)]
		if !ok {
			return "", fmt.Errorf("reference %q is not in the artifact or phase projection", ref)
		}
		return kind, nil
	}
	for _, relation := range delta.Relations {
		if !epistemic.CanAssertRelation(artifact.Phase, relation.Kind) {
			return fmt.Errorf("phase %q cannot assert relation %q", artifact.Phase, relation.Kind)
		}
		source, err := resolve(relation.Source)
		if err != nil {
			return err
		}
		target, err := resolve(relation.Target)
		if err != nil {
			return err
		}
		if !epistemic.ValidRelationEndpoints(relation.Kind, source, target) {
			return fmt.Errorf("artifact relation %q cannot connect %s to %s%s", relation.Kind, source, target, relationEndpointHint(relation.Kind, source))
		}
	}
	if delta.LeadingHypothesis != "" {
		kind, err := resolve(delta.LeadingHypothesis)
		if err != nil {
			return err
		}
		if kind != epistemic.ObjectHypothesis {
			return errors.New("leading reference is not a hypothesis")
		}
	}
	if delta.ActiveFrame != "" {
		kind, err := resolve(delta.ActiveFrame)
		if err != nil {
			return err
		}
		if kind != epistemic.ObjectFrame {
			return errors.New("active reference is not a frame")
		}
	}
	return nil
}

func relationEndpointHint(kind epistemic.RelationKind, source epistemic.ObjectKind) string {
	targets := []string{}
	for _, spec := range epistemic.RelationSpecifications() {
		if spec.Kind != kind {
			continue
		}

		for _, endpoint := range spec.Endpoints {
			if endpoint.Source == source {
				seen := slices.Contains(targets, string(endpoint.Target))

				if !seen {
					targets = append(targets, string(endpoint.Target))
				}
			}
		}
	}

	if len(targets) == 0 {
		return ""
	}

	return "; allowed targets: " + strings.Join(targets, ", ")
}

func appendArtifactObservations(destination []epistemic.ObservationInput, observations []ArtifactObservation) []epistemic.ObservationInput {
	for _, observation := range observations {
		destination = append(
			destination,
			epistemic.ObservationInput{
				LocalRef: observation.LocalRef,
				Content:  observation.Content,
			},
		)
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

// ProgressReport exposes a model report without upgrading it to execution
// evidence. Callers must use VerifiedProgress for Ghost-verified state.
func (artifact PhaseArtifact) ProgressReport() *ProgressReport {
	if value, ok := artifact.value.(ExecutionArtifact); ok && value.ReportedProgress != nil {
		report := *value.ReportedProgress
		return &report
	}
	return nil
}

// CompletionRecommended reports the closure artifact's recommendation. It is
// evidence for the orchestrator, not a decision: only CES can complete work.
func (artifact PhaseArtifact) CompletionRecommended() bool {
	value, ok := artifact.value.(ClosureArtifact)
	return ok && value.CompletionRecommended
}

func (artifact PhaseArtifact) ClosureResidualUncertainty() string {
	value, ok := artifact.value.(ClosureArtifact)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value.ResidualUncertainty)
}
