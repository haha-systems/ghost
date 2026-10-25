package epistemic

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// IsRepairableCommitError identifies errors that describe the submitted
// artifact rather than the current epistemic state or runtime.
func IsRepairableCommitError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"invalid local ref", "duplicate local ref", "relation local ref", "empty",
		"cannot create", "cannot assert relation", "cannot connect", "reference ",
		"leading reference", "active reference", "producer phase",
		"projection phase", "outside the phase projection", "claim text",
		"hypothesis mechanism", "unknown question", "constraint text",
		"frame name and summary", "action description", "outcome description",
		"has no supporting source",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

type Store struct {
	state  State
	events []Event
}

// commitPlan contains the fully validated event batches. Commit builds this
// plan before touching canonical state, then applies it in one ordered step.
type commitPlan struct {
	created, relations, retractions []Event
}

func (p commitPlan) appendTo(store *Store, result CommitResult) (CommitResult, error) {
	for _, event := range p.created {
		if event.Kind == EventObjectCreated {
			if err := store.append(event); err != nil {
				return CommitResult{}, err
			}
		}
	}
	for _, event := range p.relations {
		if err := store.append(event); err != nil {
			return CommitResult{}, err
		}
	}
	for _, event := range p.created {
		if event.Kind != EventObjectCreated {
			if err := store.append(event); err != nil {
				return CommitResult{}, err
			}
		}
	}
	for _, event := range p.retractions {
		if err := store.append(event); err != nil {
			return CommitResult{}, err
		}
	}
	for _, event := range p.created {
		if event.ObjectID != "" {
			result.Events = append(result.Events, event)
		}
	}
	result.Events = append(result.Events, p.relations...)
	result.Events = append(result.Events, p.retractions...)
	return result, nil
}

// ValidateCommit runs the complete commit contract against a replayed store.
// It is used by the phase runner to return repairable artifact errors while
// keeping canonical state untouched.
func (s *Store) ValidateCommit(request CommitRequest) error {
	if s == nil {
		return errors.New("store is nil")
	}
	copy, err := Replay(s.Events())
	if err != nil {
		return fmt.Errorf("replay store for validation: %w", err)
	}
	_, err = copy.Commit(request)
	return err
}

func NewStore(goal string) (*Store, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil, errors.New("task goal is empty")
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	task := Task{ID: id, Goal: goal, Status: WorkActive, Phase: PhaseTriage, CreatedAt: now, UpdatedAt: now}
	store := &Store{}
	taskJSON, err := marshalJSON(task)
	if err != nil {
		return nil, err
	}
	if err := store.append(Event{Kind: EventTaskCreated, ObjectKind: "task", ObjectID: id, At: now, Payload: taskJSON}); err != nil {
		return nil, err
	}
	return store, nil
}

func Replay(events []Event) (*Store, error) {
	store := &Store{}
	for i, event := range events {
		if event.Sequence != uint64(i+1) {
			return nil, fmt.Errorf("event sequence %d at position %d", event.Sequence, i+1)
		}
		if err := store.apply(event); err != nil {
			return nil, fmt.Errorf("replay event %d: %w", event.Sequence, err)
		}
		event.Payload = append(json.RawMessage(nil), event.Payload...)
		store.events = append(store.events, event)
	}
	if store.state.Task.ID == "" {
		return nil, errors.New("event stream has no task")
	}
	return store, nil
}

func (s *Store) Commit(request CommitRequest) (CommitResult, error) {
	plan, result, err := s.buildCommitPlan(request)
	if err != nil {
		return CommitResult{}, err
	}
	return plan.appendTo(s, result)
}

// buildCommitPlan validates one commit and plans its events. Commit remains
// the only writer: it appends this plan after it returns.
func (s *Store) buildCommitPlan(request CommitRequest) (commitPlan, CommitResult, error) {
	if request.Phase != s.state.Task.Phase || s.state.Task.Status != WorkActive {
		return commitPlan{}, CommitResult{}, fmt.Errorf("phase %q is not active", request.Phase)
	}
	if request.Producer.Phase != request.Phase || strings.TrimSpace(request.Producer.Process) == "" || strings.TrimSpace(request.Producer.Artifact) == "" {
		return commitPlan{}, CommitResult{}, errors.New("producer phase, process, and artifact are required")
	}
	if request.Projection.Phase != "" && request.Projection.Phase != request.Phase {
		return commitPlan{}, CommitResult{}, errors.New("projection phase does not match commit phase")
	}
	if request.Delta.WorkStatus != nil || request.Delta.TerminalReason != "" {
		return commitPlan{}, CommitResult{}, errors.New("work status is controlled by the orchestrator")
	}
	if deltaEmpty(request.Delta) {
		return commitPlan{}, CommitResult{}, errors.New("empty delta")
	}
	revision, err := newID()
	if err != nil {
		return commitPlan{}, CommitResult{}, err
	}
	planner := &commitPlanner{
		store: s, request: request, revision: revision, now: time.Now().UTC(),
		result:       CommitResult{IDs: map[string]ID{}},
		localKinds:   map[string]ObjectKind{},
		localIDs:     map[string]ID{},
		localObjects: map[ID]ObjectKind{},
	}
	if err := planner.plan(); err != nil {
		return commitPlan{}, CommitResult{}, err
	}
	return commitPlan{created: planner.created, relations: planner.relationEvents, retractions: planner.retractions}, planner.result, nil
}

func deltaEmpty(delta Delta) bool {
	return len(delta.Observations)+len(delta.Claims)+len(delta.Hypotheses)+len(delta.Unknowns)+len(delta.Constraints)+len(delta.Frames)+len(delta.Actions)+len(delta.Outcomes)+len(delta.Relations)+len(delta.Retractions)+len(delta.StatusChanges) == 0 && delta.LeadingHypothesis == "" && delta.ActiveFrame == "" && delta.WorkStatus == nil && delta.TerminalReason == ""
}

func validRelationEndpoints(kind RelationKind, source, target ObjectKind) bool {
	return ValidRelationEndpoints(kind, source, target)
}

func (s *Store) prepareStatusEvent(events *[]Event, revision ID, at time.Time, kind ObjectKind, id ID, to string) error {
	var from string
	switch kind {
	case ObjectHypothesis:
		for _, value := range s.state.Hypotheses {
			if value.ID == id {
				from = string(value.Status)
				break
			}
		}
	case ObjectUnknown:
		for _, value := range s.state.Unknowns {
			if value.ID == id {
				from = string(value.Status)
				break
			}
		}
	case ObjectFrame:
		for _, value := range s.state.Frames {
			if value.ID == id {
				from = string(value.Status)
				break
			}
		}
	default:
		return fmt.Errorf("status changes are not supported for %s", kind)
	}
	if from == "" {
		for _, event := range *events {
			if event.Kind != EventObjectCreated || event.ObjectID != id {
				continue
			}
			var payload ObjectCreatedPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return err
			}
			switch kind {
			case ObjectHypothesis:
				var value Hypothesis
				if err := json.Unmarshal(payload.Object, &value); err != nil {
					slog.Error("decode store object", "kind", kind, "error", err)
					return fmt.Errorf("decode %s status: %w", kind, err)
				}
				from = string(value.Status)
			case ObjectUnknown:
				var value Unknown
				if err := json.Unmarshal(payload.Object, &value); err != nil {
					slog.Error("decode store object", "kind", kind, "error", err)
					return fmt.Errorf("decode %s status: %w", kind, err)
				}
				from = string(value.Status)
			case ObjectFrame:
				var value Frame
				if err := json.Unmarshal(payload.Object, &value); err != nil {
					slog.Error("decode store object", "kind", kind, "error", err)
					return fmt.Errorf("decode %s status: %w", kind, err)
				}
				from = string(value.Status)
			}
			break
		}
	}
	if !validStatusTransition(kind, from, to) {
		return fmt.Errorf("invalid %s status transition %q -> %q", kind, from, to)
	}
	eventKind := EventObjectStatusChanged
	if kind == ObjectUnknown {
		eventKind = EventUnknownStatusChanged
	}
	if kind == ObjectFrame {
		eventKind = EventFrameStatusChanged
	}
	statusJSON, err := marshalJSON(objectStatusPayload{Kind: kind, ID: id, From: from, To: to})
	if err != nil {
		return err
	}
	*events = append(*events, Event{Kind: eventKind, ObjectKind: kind, ObjectID: id, RevisionID: revision, At: at, Payload: statusJSON})
	return nil
}

func validStatusTransition(kind ObjectKind, from, to string) bool {
	switch kind {
	case ObjectHypothesis:
		allowed := map[string][]string{
			string(HypothesisProposed): {string(HypothesisLive), string(HypothesisLeading), string(HypothesisRejected)},
			string(HypothesisLive):     {string(HypothesisLeading), string(HypothesisWeakened), string(HypothesisRejected), string(HypothesisSuperseded), string(HypothesisConfirmed)},
			string(HypothesisLeading):  {string(HypothesisLive), string(HypothesisWeakened), string(HypothesisRejected), string(HypothesisSuperseded), string(HypothesisConfirmed)},
			string(HypothesisWeakened): {string(HypothesisLive), string(HypothesisLeading), string(HypothesisRejected), string(HypothesisSuperseded)},
		}
		for _, value := range allowed[from] {
			if value == to {
				return true
			}
		}
	case ObjectUnknown:
		return (from == string(UnknownOpen) || from == string(UnknownReopened)) && to == string(UnknownResolved) || from == string(UnknownResolved) && to == string(UnknownReopened)
	case ObjectFrame:
		return (from == string(FramePending) && to == string(FrameActive)) || (from == string(FrameActive) && to == string(FrameSuperseded))
	}
	return false
}

func validateStatusChange(phase Phase, state State, kind ObjectKind, id ID, status string) error {
	if kind == ObjectObservation {
		return errors.New("observations are immutable")
	}
	if kind == ObjectHypothesis && phase != PhaseAbduce && (phase != PhaseClose || status != string(HypothesisConfirmed)) {
		return errors.New("phase cannot change hypothesis status")
	}
	if kind == ObjectHypothesis && phase == PhaseClose && status == string(HypothesisConfirmed) {
		return nil
	}
	if kind == ObjectUnknown && phase != PhaseTriage && phase != PhaseAbduce && phase != PhaseExecute && phase != PhaseClose {
		return errors.New("phase cannot change unknown status")
	}
	if kind == ObjectFrame {
		return errors.New("frames change status only when superseded by a new frame")
	}
	if kind == ObjectUnknown {
		for _, value := range state.Unknowns {
			if value.ID == id && value.Status == UnknownResolved && status == string(UnknownReopened) && phase == PhaseTriage {
				return nil
			}
		}
	}
	return errors.New("unsupported status change")
}

func (s *Store) State() State { return cloneState(s.state) }

func (s *Store) Events() []Event {
	out := make([]Event, len(s.events))
	for i, event := range s.events {
		out[i] = event
		out[i].Payload = append(json.RawMessage(nil), event.Payload...)
	}
	return out
}

func (s *Store) append(event Event) error {
	event.Sequence = uint64(len(s.events) + 1)
	if event.WorkID == "" {
		if event.Kind == EventTaskCreated {
			event.WorkID = string(event.ObjectID)
		} else {
			event.WorkID = string(s.state.Task.ID)
		}
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if err := s.apply(event); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

func (s *Store) apply(event Event) error {
	if err := applyEvent(&s.state, event); err != nil {
		return err
	}
	return nil
}

func marshalJSON(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		slog.Error("encode store payload", "error", err)
		return nil, fmt.Errorf("encode store payload: %w", err)
	}
	return data, nil
}
