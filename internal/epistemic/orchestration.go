package epistemic

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const MaxReopens = 3

func (s *Store) TransitionPhase(request TransitionRequest) (TransitionResult, error) {
	from := s.state.Task.Phase
	if s.state.Task.Status != WorkActive {
		return TransitionResult{}, errors.New("work is terminal")
	}
	if strings.TrimSpace(request.Reason) == "" {
		return TransitionResult{}, errors.New("phase transition reason is empty")
	}
	toIndex, fromIndex := phaseIndex(request.To), phaseIndex(from)
	if toIndex < 0 || fromIndex < 0 {
		return TransitionResult{}, errors.New("unknown phase")
	}
	reopenCount := s.state.Task.ReopenCount
	if toIndex == fromIndex+1 {
		if request.TargetID != "" || request.TargetKind != "" {
			return TransitionResult{}, errors.New("forward transition cannot name a contradiction target")
		}
	} else if toIndex < fromIndex {
		if request.TargetKind != ObjectHypothesis && request.TargetKind != ObjectFrame && request.TargetKind != ObjectAction && request.TargetKind != ObjectOutcome {
			return TransitionResult{}, errors.New("backward transition needs a contradicted hypothesis, frame, action, or outcome")
		}
		if routeForTarget(request.TargetKind) != request.To {
			return TransitionResult{}, fmt.Errorf("%s contradiction routes to %s", request.TargetKind, routeForTarget(request.TargetKind))
		}
		if !s.hasActiveContradiction(request.TargetID, request.TargetKind) {
			return TransitionResult{}, errors.New("backward transition target has no active contradiction")
		}
		if request.TargetKind == ObjectHypothesis && request.TargetID != s.state.Task.LeadingHypothesis {
			return TransitionResult{}, errors.New("only the leading hypothesis can reopen abduction")
		}
		if request.TargetKind == ObjectFrame && request.TargetID != s.state.Task.ActiveFrame {
			return TransitionResult{}, errors.New("only the active frame can reopen framing")
		}
		if reopenCount >= MaxReopens {
			return s.SetTerminal(WorkIncomplete, "reopen_budget_exhausted")
		}
		reopenCount++
	} else {
		return TransitionResult{}, fmt.Errorf("invalid phase transition %s -> %s", from, request.To)
	}
	event := Event{Kind: EventPhaseChanged, At: time.Now().UTC(), ObjectKind: "task", ObjectID: s.state.Task.ID, Payload: mustJSON(phasePayload{From: from, To: request.To, Reason: request.Reason, TargetID: request.TargetID, TargetKind: request.TargetKind, ReopenCount: reopenCount})}
	if err := s.append(event); err != nil {
		return TransitionResult{}, err
	}
	return TransitionResult{Events: s.Events()[len(s.events)-1:]}, nil
}

func (s *Store) SetTerminal(status WorkStatus, reason string) (TransitionResult, error) {
	if s.state.Task.Status != WorkActive {
		return TransitionResult{}, errors.New("work is already terminal")
	}
	if status == WorkComplete {
		if s.state.Task.Phase != PhaseClose {
			return TransitionResult{}, errors.New("work can complete only in close phase")
		}
		if s.state.Task.LeadingHypothesis == "" || !s.hypothesisHasStatus(s.state.Task.LeadingHypothesis, HypothesisConfirmed) {
			return TransitionResult{}, errors.New("completion needs a confirmed leading hypothesis")
		}
		if s.hasBlockingContradiction() {
			return TransitionResult{}, errors.New("active contradiction prevents completion")
		}
		reason = ""
	} else if status == WorkIncomplete {
		if !validIncompleteReason(reason) {
			return TransitionResult{}, fmt.Errorf("invalid incomplete reason %q", reason)
		}
	} else {
		return TransitionResult{}, fmt.Errorf("invalid terminal status %q", status)
	}
	now := time.Now().UTC()
	start := len(s.events)
	statusEvent := Event{Kind: EventWorkStatusChanged, At: now, ObjectKind: "task", ObjectID: s.state.Task.ID, Payload: mustJSON(workStatusPayload{From: WorkActive, To: status, Reason: reason})}
	if err := s.append(statusEvent); err != nil {
		return TransitionResult{}, err
	}
	if reason != "" {
		event := Event{Kind: EventTerminalReasonRecorded, At: now, ObjectKind: "task", ObjectID: s.state.Task.ID, Payload: mustJSON(reason)}
		if err := s.append(event); err != nil {
			return TransitionResult{}, err
		}
	}
	return TransitionResult{Events: s.Events()[start:]}, nil
}

func phaseIndex(phase Phase) int {
	switch phase {
	case PhaseTriage:
		return 0
	case PhaseAbduce:
		return 1
	case PhaseFrame:
		return 2
	case PhaseExecute:
		return 3
	case PhaseClose:
		return 4
	default:
		return -1
	}
}

func routeForTarget(kind ObjectKind) Phase {
	switch kind {
	case ObjectHypothesis:
		return PhaseAbduce
	case ObjectFrame:
		return PhaseFrame
	case ObjectAction, ObjectOutcome:
		return PhaseExecute
	default:
		return ""
	}
}

func (s *Store) hasActiveContradiction(id ID, kind ObjectKind) bool {
	if id == "" {
		return false
	}
	actual, ok := objectKind(s.state, id)
	if !ok || actual != kind {
		return false
	}
	for _, relation := range s.state.Relations {
		if relation.Status == RelationActive && relation.Kind == RelationContradicts && relation.TargetID == id {
			return true
		}
	}
	return false
}

func (s *Store) hypothesisHasStatus(id ID, status HypothesisStatus) bool {
	for _, value := range s.state.Hypotheses {
		if value.ID == id {
			return value.Status == status
		}
	}
	return false
}

func (s *Store) hasBlockingContradiction() bool {
	for _, relation := range s.state.Relations {
		if relation.Status != RelationActive || relation.Kind != RelationContradicts {
			continue
		}
		if relation.TargetID == s.state.Task.LeadingHypothesis || relation.TargetID == s.state.Task.ActiveFrame {
			return true
		}
		kind, ok := objectKind(s.state, relation.TargetID)
		if ok && (kind == ObjectAction || kind == ObjectOutcome) {
			return true
		}
	}
	return false
}

func validIncompleteReason(reason string) bool {
	switch reason {
	case "reopen_budget_exhausted", "tool_unavailable", "cognitive_resource_unavailable", "token_budget_exhausted", "wall_clock_budget_exhausted", "underdetermined", "phase_execution_failed":
		return true
	default:
		return false
	}
}
