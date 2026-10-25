package core

import (
	"fmt"
	"math"
	"time"
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskWaiting   TaskStatus = "waiting"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

func (s TaskStatus) IsTerminal() bool {
	return s == TaskCompleted || s == TaskFailed || s == TaskCancelled
}

type CESPolicy string

const (
	CESOff     CESPolicy = "off"
	CESObserve CESPolicy = "observe"
	CESEnforce CESPolicy = "enforce"
)

type CESPhase string

const (
	CESPhaseTriage  CESPhase = "triage"
	CESPhaseAbduce  CESPhase = "abduce"
	CESPhaseFrame   CESPhase = "frame"
	CESPhaseExecute CESPhase = "execute"
	CESPhaseClose   CESPhase = "close"
)

type TaskBudget struct {
	MaxTokens   int64         `json:"max_tokens,omitempty"`
	MaxCost     float64       `json:"max_cost,omitempty"`
	MaxDuration time.Duration `json:"max_duration,omitempty"`
	Deadline    *time.Time    `json:"deadline,omitempty"`
}

type TaskLimits struct {
	MaxDelegationDepth int        `json:"max_delegation_depth,omitempty"`
	MaxParallel        int        `json:"max_parallel,omitempty"`
	Budget             TaskBudget `json:"budget,omitempty"`
}

// Task is one unit of runtime work and its lifecycle timestamps.
type Task struct {
	ID           TaskID     `json:"id"`
	Requester    AgentID    `json:"requester"`
	Owner        AgentID    `json:"owner"`
	Parent       *TaskID    `json:"parent,omitempty"`
	Participants []AgentID  `json:"participants,omitempty"`
	Status       TaskStatus `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	DependsOn    []TaskID   `json:"depends_on,omitempty"`
	CESPolicy    CESPolicy  `json:"ces_policy"`
	CESPhase     CESPhase   `json:"ces_phase,omitempty"`
	Limits       TaskLimits `json:"limits,omitempty"`
	ThreadID     ThreadID   `json:"thread_id"`
	EventRefs    []EventID  `json:"event_refs,omitempty"`
}

var taskTransitions = map[TaskStatus]map[TaskStatus]struct{}{
	TaskQueued:  {TaskRunning: {}, TaskWaiting: {}, TaskFailed: {}, TaskCancelled: {}},
	TaskRunning: {TaskWaiting: {}, TaskCompleted: {}, TaskFailed: {}, TaskCancelled: {}},
	TaskWaiting: {TaskRunning: {}, TaskCompleted: {}, TaskFailed: {}, TaskCancelled: {}},
}

// CanTransitionTask reports whether the contract permits a state change.
func CanTransitionTask(from, to TaskStatus) bool {
	_, ok := taskTransitions[from][to]
	return ok
}

func (t *Task) Transition(to TaskStatus, now time.Time) error {
	if t == nil {
		return fmt.Errorf("nil task")
	}
	if err := t.Validate(); err != nil {
		return fmt.Errorf("invalid current task: %w", err)
	}
	if !CanTransitionTask(t.Status, to) {
		return fmt.Errorf("invalid task transition %q -> %q", t.Status, to)
	}
	if now.IsZero() || now.Before(t.UpdatedAt) {
		return fmt.Errorf("transition time must be set and not precede updated_at")
	}
	now = now.UTC()
	candidate := *t
	if to == TaskRunning && t.StartedAt == nil {
		started := now
		candidate.StartedAt = &started
	}
	if to.IsTerminal() {
		finished := now
		candidate.FinishedAt = &finished
	}
	candidate.Status = to
	candidate.UpdatedAt = now
	if err := candidate.Validate(); err != nil {
		return err
	}
	*t = candidate
	return nil
}

func (t Task) Validate() error {
	if err := t.ID.Validate(); err != nil {
		return fmt.Errorf("task ID: %w", err)
	}
	if t.Requester != HumanAgentID {
		if err := t.Requester.Validate(); err != nil {
			return fmt.Errorf("requester: %w", err)
		}
	}
	if err := t.Owner.Validate(); err != nil {
		return fmt.Errorf("owner: %w", err)
	}
	if t.Parent != nil {
		if err := t.Parent.Validate(); err != nil {
			return fmt.Errorf("parent: %w", err)
		}
	}
	if err := validateAgentIDs(t.Participants); err != nil {
		return fmt.Errorf("participant: %w", err)
	}
	switch t.Status {
	case TaskQueued, TaskRunning, TaskWaiting, TaskCompleted, TaskFailed, TaskCancelled:
	default:
		return fmt.Errorf("unknown task status %q", t.Status)
	}
	if t.CreatedAt.IsZero() || t.UpdatedAt.IsZero() || t.UpdatedAt.Before(t.CreatedAt) {
		return fmt.Errorf("task created_at and updated_at must be set and ordered")
	}
	if t.StartedAt != nil && (t.StartedAt.IsZero() || t.StartedAt.Before(t.CreatedAt) || t.StartedAt.After(t.UpdatedAt)) {
		return fmt.Errorf("task started_at is outside its lifecycle")
	}
	if t.Status == TaskRunning && t.StartedAt == nil {
		return fmt.Errorf("running task has no started_at")
	}
	if t.Status == TaskQueued && t.StartedAt != nil {
		return fmt.Errorf("queued task has started_at")
	}
	if t.Status.IsTerminal() != (t.FinishedAt != nil) {
		return fmt.Errorf("terminal task must have finished_at and active task must not")
	}
	if t.FinishedAt != nil && (t.FinishedAt.IsZero() || t.FinishedAt.After(t.UpdatedAt) || (t.StartedAt != nil && t.FinishedAt.Before(*t.StartedAt))) {
		return fmt.Errorf("task finished_at is outside its lifecycle")
	}
	if t.Status == TaskCompleted && t.StartedAt == nil {
		return fmt.Errorf("completed task has no started_at")
	}
	if t.ThreadID != "" {
		if err := t.ThreadID.Validate(); err != nil {
			return fmt.Errorf("thread ID: %w", err)
		}
	}
	if t.ThreadID == "" {
		return fmt.Errorf("task thread ID is required")
	}
	if t.Parent != nil && *t.Parent == t.ID {
		return fmt.Errorf("task cannot be its own parent")
	}
	if err := validateTaskIDs(t.DependsOn); err != nil {
		return err
	}
	if _, exists := slicesContains(t.DependsOn, t.ID); exists {
		return fmt.Errorf("task cannot depend on itself")
	}
	switch t.CESPolicy {
	case CESOff, CESObserve, CESEnforce:
	default:
		return fmt.Errorf("unknown CES policy %q", t.CESPolicy)
	}
	if t.CESPhase != "" {
		switch t.CESPhase {
		case CESPhaseTriage, CESPhaseAbduce, CESPhaseFrame, CESPhaseExecute, CESPhaseClose:
		default:
			return fmt.Errorf("unknown CES phase %q", t.CESPhase)
		}
		if t.CESPolicy == CESOff {
			return fmt.Errorf("CES phase requires an enabled CES policy")
		}
	}
	if t.Limits.MaxDelegationDepth < 0 || t.Limits.MaxParallel < 0 || t.Limits.Budget.MaxTokens < 0 || t.Limits.Budget.MaxDuration < 0 {
		return fmt.Errorf("task limits cannot be negative")
	}
	if math.IsNaN(t.Limits.Budget.MaxCost) || math.IsInf(t.Limits.Budget.MaxCost, 0) || t.Limits.Budget.MaxCost < 0 {
		return fmt.Errorf("task budget max_cost must be finite and non-negative")
	}
	if t.Limits.Budget.Deadline != nil && t.Limits.Budget.Deadline.IsZero() {
		return fmt.Errorf("task budget deadline cannot be zero")
	}
	seenEvents := map[EventID]struct{}{}
	for _, id := range t.EventRefs {
		if err := id.Validate(); err != nil {
			return fmt.Errorf("event reference: %w", err)
		}
		if _, exists := seenEvents[id]; exists {
			return fmt.Errorf("duplicate event reference %q", id)
		}
		seenEvents[id] = struct{}{}
	}
	return nil
}

func validateTaskIDs(ids []TaskID) error {
	seen := map[TaskID]struct{}{}
	for _, id := range ids {
		if err := id.Validate(); err != nil {
			return fmt.Errorf("dependency: %w", err)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate dependency %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func slicesContains[T comparable](values []T, item T) (int, bool) {
	for i, value := range values {
		if value == item {
			return i, true
		}
	}
	return 0, false
}
