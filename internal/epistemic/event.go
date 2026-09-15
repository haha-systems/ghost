package epistemic

import (
	"encoding/json"
	"time"
)

type EventKind string

const (
	EventTaskCreated            EventKind = "TaskCreated"
	EventObjectCreated          EventKind = "ObjectCreated"
	EventObjectStatusChanged    EventKind = "ObjectStatusChanged"
	EventUnknownStatusChanged   EventKind = "UnknownStatusChanged"
	EventFrameStatusChanged     EventKind = "FrameStatusChanged"
	EventRelationCreated        EventKind = "RelationCreated"
	EventRelationStatusChanged  EventKind = "RelationStatusChanged"
	EventPhaseChanged           EventKind = "PhaseChanged"
	EventWorkStatusChanged      EventKind = "WorkStatusChanged"
	EventTerminalReasonRecorded EventKind = "TerminalReasonRecorded"
)

type Event struct {
	Sequence   uint64
	Kind       EventKind
	WorkID     string
	ObjectKind ObjectKind
	ObjectID   ID
	RevisionID ID
	At         time.Time
	Payload    json.RawMessage
}

type ObjectCreatedPayload struct {
	Kind   ObjectKind
	Object json.RawMessage
}

type objectStatusPayload struct {
	Kind     ObjectKind
	ID       ID
	From, To string
}

type relationStatusPayload struct {
	ID        ID
	From, To  RelationStatus
	ReasonRef ID
}

type phasePayload struct {
	From, To    Phase
	Reason      string
	TargetID    ID
	TargetKind  ObjectKind
	ReopenCount int
}

type workStatusPayload struct {
	From, To WorkStatus
	Reason   string
}

type SemanticEventKind string

const (
	SemanticPhaseChanged            SemanticEventKind = "PhaseChanged"
	SemanticHypothesisBecameLeading SemanticEventKind = "HypothesisBecameLeading"
	SemanticHypothesisRejected      SemanticEventKind = "HypothesisRejected"
	SemanticFrameActivated          SemanticEventKind = "FrameActivated"
	SemanticContradictionRecorded   SemanticEventKind = "ContradictionRecorded"
	SemanticWorkReopened            SemanticEventKind = "WorkReopened"
	SemanticWorkCompleted           SemanticEventKind = "WorkCompleted"
	SemanticWorkIncomplete          SemanticEventKind = "WorkIncomplete"
)

type SemanticEvent struct {
	Kind       SemanticEventKind
	At         time.Time
	WorkID     string
	From, To   Phase
	ObjectID   ID
	ObjectKind ObjectKind
	Reason     string
}
