package epistemic

import "time"

type Producer struct {
	Phase         Phase
	Process       string
	Artifact      string
	Runtime       string
	Model         string
	Deterministic bool
	Transform     string
	Version       string
}

type Task struct {
	ID                ID
	Goal              string
	Status            WorkStatus
	Phase             Phase
	ReopenCount       int
	TerminalReason    string
	LeadingHypothesis ID
	ActiveFrame       ID
	LastTransition    *TransitionSummary
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Observation struct {
	ID         ID
	Content    string
	ProducedBy Producer
	RevisionID ID
	CreatedAt  time.Time
}

type Claim struct {
	ID         ID
	Text       string
	ProducedBy Producer
	RevisionID ID
	CreatedAt  time.Time
}

type HypothesisStatus string

const (
	HypothesisProposed   HypothesisStatus = "proposed"
	HypothesisLive       HypothesisStatus = "live"
	HypothesisLeading    HypothesisStatus = "leading"
	HypothesisWeakened   HypothesisStatus = "weakened"
	HypothesisRejected   HypothesisStatus = "rejected"
	HypothesisSuperseded HypothesisStatus = "superseded"
	HypothesisConfirmed  HypothesisStatus = "confirmed"
)

type Hypothesis struct {
	ID         ID
	Mechanism  string
	Falsifier  string
	Status     HypothesisStatus
	ProducedBy Producer
	RevisionID ID
	CreatedAt  time.Time
}

type UnknownStatus string

const (
	UnknownOpen     UnknownStatus = "open"
	UnknownResolved UnknownStatus = "resolved"
	UnknownReopened UnknownStatus = "reopened"
)

type Unknown struct {
	ID         ID
	Question   string
	Status     UnknownStatus
	ProducedBy Producer
	RevisionID ID
	CreatedAt  time.Time
}

type Constraint struct {
	ID         ID
	Text       string
	ProducedBy Producer
	RevisionID ID
	CreatedAt  time.Time
}

type FrameStatus string

const (
	FrameActive     FrameStatus = "active"
	FrameSuperseded FrameStatus = "superseded"
)

type Frame struct {
	ID                        ID
	Name                      string
	Summary                   string
	CompletionConditions      []string
	DisconfirmationConditions []string
	Status                    FrameStatus
	ProducedBy                Producer
	RevisionID                ID
	CreatedAt                 time.Time
}

type Action struct {
	ID          ID
	Description string
	Status      string
	ProducedBy  Producer
	RevisionID  ID
	CreatedAt   time.Time
}

type Outcome struct {
	ID          ID
	Description string
	ProducedBy  Producer
	RevisionID  ID
	CreatedAt   time.Time
}

type RelationKind string

const (
	RelationSupports    RelationKind = "supports"
	RelationContradicts RelationKind = "contradicts"
	RelationDerivedFrom RelationKind = "derived_from"
	RelationDependsOn   RelationKind = "depends_on"
	RelationImplements  RelationKind = "implements"
	RelationTests       RelationKind = "tests"
	RelationResolves    RelationKind = "resolves"
	RelationSupersedes  RelationKind = "supersedes"
)

type RelationStatus string

const (
	RelationActive    RelationStatus = "active"
	RelationRetracted RelationStatus = "retracted"
)

type Relation struct {
	ID         ID
	Kind       RelationKind
	SourceID   ID
	TargetID   ID
	Status     RelationStatus
	ReasonRef  ID
	ProducedBy Producer
	RevisionID ID
	CreatedAt  time.Time
}

type State struct {
	Task         Task
	Observations []Observation
	Claims       []Claim
	Hypotheses   []Hypothesis
	Unknowns     []Unknown
	Constraints  []Constraint
	Frames       []Frame
	Actions      []Action
	Outcomes     []Outcome
	Relations    []Relation
}

type ObservationInput struct {
	LocalRef, Content string
	DerivedFrom       string
}
type ClaimInput struct{ LocalRef, Text string }
type HypothesisInput struct{ LocalRef, Mechanism, Falsifier string }
type UnknownInput struct{ LocalRef, Question string }
type ConstraintInput struct{ LocalRef, Text string }
type FrameInput struct {
	LocalRef, Name, Summary                         string
	CompletionConditions, DisconfirmationConditions []string
}
type ActionInput struct{ LocalRef, Description string }
type OutcomeInput struct{ LocalRef, Description string }
type RelationInput struct {
	LocalRef       string
	Kind           RelationKind
	Source, Target string
}
type RetractionInput struct{ Relation, ReasonRef string }
type StatusChange struct {
	Kind        ObjectKind
	Ref, Status string
}

type Delta struct {
	Observations      []ObservationInput
	Claims            []ClaimInput
	Hypotheses        []HypothesisInput
	Unknowns          []UnknownInput
	Constraints       []ConstraintInput
	Frames            []FrameInput
	Actions           []ActionInput
	Outcomes          []OutcomeInput
	Relations         []RelationInput
	Retractions       []RetractionInput
	StatusChanges     []StatusChange
	LeadingHypothesis string
	ActiveFrame       string
	WorkStatus        *WorkStatus
	TerminalReason    string
}

type CommitRequest struct {
	Phase      Phase
	Producer   Producer
	Delta      Delta
	Projection Projection
}

type Projection struct {
	Phase Phase
	State State
	IDs   []ID
}

type ProjectionRequest struct {
	Phase   Phase
	Include []ID
}

type TransitionRequest struct {
	To         Phase
	Reason     string
	TargetID   ID
	TargetKind ObjectKind
}

type TransitionResult struct{ Events []Event }

func (p Projection) Contains(id ID) bool {
	for _, included := range p.IDs {
		if included == id {
			return true
		}
	}
	return false
}

type CommitResult struct {
	IDs    map[string]ID
	Events []Event
}
