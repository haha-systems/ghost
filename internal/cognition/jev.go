package cognition

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/haha-systems/ghost/internal/epistemic"
)

// ProgressState is Ghost's evidence-aware finding about forward movement. It
// is deliberately separate from artifact validity and a model's own report.
type ProgressState string

const (
	ProgressUnknown    ProgressState = "unknown"
	ProgressLow        ProgressState = "low"
	ProgressMeaningful ProgressState = "meaningful"
)

// ProgressProvenance identifies the source class, not free-text evidence.
type ProgressProvenance string

const (
	ProgressMissing             ProgressProvenance = "missing"
	ProgressModelReported       ProgressProvenance = "model_reported"
	ProgressStructuredExecution ProgressProvenance = "structured_execution"
)

type ProgressEvidence struct {
	State      ProgressState
	Provenance ProgressProvenance
	Strength   float64
}

// ProgressReport is model-provided and never becomes verified progress by
// parsing or committing an artifact.
type ProgressReport struct {
	State ProgressState `json:"state"`
}

// ProgressVerifier is supplied by Ghost components that can inspect
// structured execution evidence. Model reports and artifact acceptance never
// satisfy this boundary.
type ProgressVerifier interface {
	VerifyProgress(epistemic.State, PhaseArtifact) ProgressEvidence
}

func VerifiedProgress(verifier ProgressVerifier, state epistemic.State, artifact PhaseArtifact) ProgressEvidence {
	if verifier == nil {
		return ProgressEvidence{State: ProgressUnknown, Provenance: ProgressMissing}
	}
	value := verifier.VerifyProgress(state, artifact)
	if value.Provenance != ProgressStructuredExecution || value.Strength <= 0 || value.Strength > 1 {
		return ProgressEvidence{State: ProgressUnknown, Provenance: ProgressMissing}
	}
	switch value.State {
	case ProgressLow, ProgressMeaningful:
		return value
	default:
		return ProgressEvidence{State: ProgressUnknown, Provenance: ProgressMissing}
	}
}

// CognitiveSituation is the compact allocation-boundary state. It has no
// transcript, goal text, repository text, or model output.
type CognitiveSituation struct {
	Phase                epistemic.Phase
	CurrentResource      string
	ReasoningEffort      string
	RepeatedAttempts     int
	FailedAttempts       int
	ReopenCount          int
	ArtifactValid        bool
	ArtifactAccepted     bool
	Progress             ProgressEvidence
	ReportedProgress     *ProgressReport
	OpenUnknownCount     int
	ActiveContradictions int
	HasLeadingHypothesis bool
	HasActiveFrame       bool
	ScopeComplexity      float64
}

type JevInput struct {
	Phase                epistemic.Phase    `json:"phase"`
	CurrentResource      string             `json:"current_resource"`
	ReasoningEffort      string             `json:"reasoning_effort,omitempty"`
	RepeatedAttempts     int                `json:"repeated_attempts"`
	FailedAttempts       int                `json:"failed_attempts"`
	ReopenCount          int                `json:"reopen_count"`
	ArtifactValid        bool               `json:"artifact_valid"`
	ArtifactAccepted     bool               `json:"artifact_accepted"`
	ProgressState        ProgressState      `json:"progress_state"`
	ProgressProvenance   ProgressProvenance `json:"progress_provenance"`
	ProgressStrength     float64            `json:"progress_strength"`
	ReportedProgress     bool               `json:"reported_progress"`
	OpenUnknownCount     int                `json:"open_unknown_count"`
	ActiveContradictions int                `json:"active_contradiction_count"`
	HasLeadingHypothesis bool               `json:"has_leading_hypothesis"`
	HasActiveFrame       bool               `json:"has_active_frame"`
	ScopeComplexity      float64            `json:"scope_complexity"`
}

func (s CognitiveSituation) jevInput(current string) JevInput {
	if s.CurrentResource == "" {
		s.CurrentResource = current
	}
	return JevInput{
		Phase: s.Phase, CurrentResource: s.CurrentResource, ReasoningEffort: s.ReasoningEffort,
		RepeatedAttempts: s.RepeatedAttempts, FailedAttempts: s.FailedAttempts, ReopenCount: s.ReopenCount,
		ArtifactValid: s.ArtifactValid, ArtifactAccepted: s.ArtifactAccepted,
		ProgressState: s.Progress.State, ProgressProvenance: s.Progress.Provenance, ProgressStrength: s.Progress.Strength,
		ReportedProgress: s.ReportedProgress != nil, OpenUnknownCount: s.OpenUnknownCount,
		ActiveContradictions: s.ActiveContradictions, HasLeadingHypothesis: s.HasLeadingHypothesis,
		HasActiveFrame: s.HasActiveFrame, ScopeComplexity: s.ScopeComplexity,
	}
}

// JevClient is an optional boundary to a cognitive-adequacy assessor. It
// returns only its compact structured judgment. QAC owns every allocation.
type JevClient interface {
	Assess(context.Context, JevInput) ([]byte, error)
}

type JevCategory string

const (
	JevContinue             JevCategory = "continue_current_resource"
	JevEscalate             JevCategory = "escalate_cognition"
	JevReframeOrDecompose   JevCategory = "reframe_or_decompose"
	JevInsufficientEvidence JevCategory = "insufficient_evidence"
)

type JevJudgment struct {
	CurrentResourceAdequacy           float64     `json:"current_resource_adequacy"`
	ExpectedGainFromStrongerCognition float64     `json:"expected_gain_from_stronger_cognition"`
	DecompositionNeed                 float64     `json:"decomposition_need"`
	Confidence                        float64     `json:"confidence"`
	Judgment                          JevCategory `json:"judgment"`
}

// JevAssessment records the compact boundary result for tracing and tests.
// Used means QAC accepted advisory score signals, not that it escalated.
type JevAssessment struct {
	Requested    bool
	Input        JevInput
	Judgment     *JevJudgment
	Used         bool
	IgnoreReason string
}

const JevContract = "Jev judges cognitive adequacy only. Escalation has a cost. Repeated attempts are evidence, not an automatic escalation rule. Accepted artifacts do not imply useful progress. Unknown progress is not failed progress. Stronger cognition and task decomposition are distinct remedies. Jev cannot choose a resource, model, provider, agent, or reasoning effort."

func ParseJevJudgment(data []byte) (JevJudgment, error) {
	var value JevJudgment
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return JevJudgment{}, fmt.Errorf("decode Jev judgment: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return JevJudgment{}, errors.New("Jev judgment has trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return JevJudgment{}, fmt.Errorf("read Jev judgment: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return JevJudgment{}, fmt.Errorf("decode Jev judgment fields: %w", err)
	}
	for _, field := range []string{"current_resource_adequacy", "expected_gain_from_stronger_cognition", "decomposition_need", "confidence", "judgment"} {
		if _, ok := fields[field]; !ok {
			return JevJudgment{}, fmt.Errorf("Jev judgment is missing %q", field)
		}
	}
	for name, score := range map[string]float64{
		"current_resource_adequacy":             value.CurrentResourceAdequacy,
		"expected_gain_from_stronger_cognition": value.ExpectedGainFromStrongerCognition,
		"decomposition_need":                    value.DecompositionNeed,
		"confidence":                            value.Confidence,
	} {
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return JevJudgment{}, fmt.Errorf("Jev %s must be in [0,1]", name)
		}
	}
	switch value.Judgment {
	case JevContinue, JevEscalate, JevReframeOrDecompose, JevInsufficientEvidence:
		return value, nil
	default:
		return JevJudgment{}, fmt.Errorf("invalid Jev judgment %q", value.Judgment)
	}
}

func shouldAssessJev(s CognitiveSituation) bool {
	return s.Phase == epistemic.PhaseExecute && s.RepeatedAttempts > 1 &&
		s.Progress.State == ProgressLow && s.Progress.Provenance == ProgressStructuredExecution &&
		s.Progress.Strength > 0
}

func applyJevAdvice(request QACRequest, judgment JevJudgment) (QACRequest, bool) {
	if judgment.Judgment != JevEscalate || judgment.Confidence == 0 {
		return request, false
	}
	request.ExpectedGain = max(request.ExpectedGain, judgment.ExpectedGainFromStrongerCognition*judgment.Confidence)
	request.Uncertainty = max(request.Uncertainty, (1-judgment.CurrentResourceAdequacy)*judgment.Confidence)
	return request, true
}
