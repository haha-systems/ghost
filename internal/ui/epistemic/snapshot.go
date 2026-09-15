package epistemic

import (
	"time"

	ces "github.com/haha-systems/ghost/internal/epistemic"
)

// Object is the read-only inspector projection of one CES object.
type Object struct {
	ID         ces.ID
	Alias      string
	Kind       ces.ObjectKind
	Label      string
	Summary    string
	Status     string
	ProducedBy string
	Revision   string
	CreatedAt  time.Time
	Falsifier  string
}

// Relation is the readable provenance projection of one CES relation.
type Relation struct {
	ID               ces.ID
	Alias            string
	Kind             string
	SourceAlias      string
	SourceID         ces.ID
	TargetAlias      string
	TargetID         ces.ID
	Status           string
	SourceSummary    string
	TargetSummary    string
	ProducedBy       string
	RetractionReason string
}

// Snapshot contains display-ready, read-only detail. The UI does not read CES
// storage and does not reconstruct state from the operator history.
type Snapshot struct {
	Operator   ces.OperatorView
	Supplement ces.OperatorSupplement
	Objects    []Object
	Relations  []Relation
}

func (s Snapshot) clone() Snapshot {
	out := s
	out.Objects = append([]Object(nil), s.Objects...)
	out.Relations = append([]Relation(nil), s.Relations...)
	if s.Operator.LeadingHypothesis != nil {
		object := *s.Operator.LeadingHypothesis
		out.Operator.LeadingHypothesis = &object
	}
	out.Supplement.InvalidatedPhases = append([]ces.Phase(nil), s.Supplement.InvalidatedPhases...)
	if s.Operator.ActiveFrame != nil {
		object := *s.Operator.ActiveFrame
		out.Operator.ActiveFrame = &object
	}
	if s.Operator.LastTransition != nil {
		transition := *s.Operator.LastTransition
		out.Operator.LastTransition = &transition
	}
	return out
}
