// Package comms provides the in-process message and task broker. It owns
// routing policy and delivery state; providers do not route model text.
package comms

import (
	"context"
	"fmt"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

// Delivery hands an accepted message to one recipient. Router calls it at
// most once for each message and recipient pair.
type Delivery interface {
	Deliver(ctx context.Context, agentID core.AgentID, message core.Message) error
}

// TaskDelivery adds child-task context to delegation delivery. It is called
// after the router releases its lock, so an adapter may safely perform I/O.
type TaskDelivery interface {
	DeliverTask(ctx context.Context, agentID core.AgentID, message core.Message, task core.Task) error
}

// AgentDirectory supplies current availability, permissions, and message
// authorization. Agent records must be detached values.
type AgentDirectory interface {
	Agent(id core.AgentID) (core.Agent, bool)
	Agents() []core.Agent
	CanMessage(sender, recipient core.AgentID) bool
}

// Limits bounds delegation, communication, delivery wait, and consultations.
type Limits struct {
	MaxDelegationDepth                 int
	MaxAgentVisits                     int
	MaxMessagesPerTask                 int
	MaxConcurrentConsultationsPerAgent int
	DefaultTimeout                     time.Duration
	MaxPendingAge                      time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxDelegationDepth:                 3,
		MaxAgentVisits:                     1,
		MaxMessagesPerTask:                 50,
		MaxConcurrentConsultationsPerAgent: 2,
		DefaultTimeout:                     10 * time.Minute,
		MaxPendingAge:                      10 * time.Minute,
	}
}

func (limits Limits) withDefaults() (Limits, error) {
	defaults := DefaultLimits()
	if limits.MaxDelegationDepth == 0 {
		limits.MaxDelegationDepth = defaults.MaxDelegationDepth
	}
	if limits.MaxAgentVisits == 0 {
		limits.MaxAgentVisits = defaults.MaxAgentVisits
	}
	if limits.MaxMessagesPerTask == 0 {
		limits.MaxMessagesPerTask = defaults.MaxMessagesPerTask
	}
	if limits.MaxConcurrentConsultationsPerAgent == 0 {
		limits.MaxConcurrentConsultationsPerAgent = defaults.MaxConcurrentConsultationsPerAgent
	}
	if limits.DefaultTimeout == 0 {
		limits.DefaultTimeout = defaults.DefaultTimeout
	}
	if limits.MaxPendingAge == 0 {
		limits.MaxPendingAge = defaults.MaxPendingAge
	}
	if limits.MaxDelegationDepth < 0 || limits.MaxAgentVisits < 1 || limits.MaxMessagesPerTask < 1 || limits.MaxConcurrentConsultationsPerAgent < 1 || limits.DefaultTimeout < 0 || limits.MaxPendingAge < 0 {
		return Limits{}, fmt.Errorf("communication limits must be positive")
	}
	return limits, nil
}

// RouterConfig injects the workspace, time source, agent directory, and
// delivery transport. A nil clock and ID source use the system defaults.
type RouterConfig struct {
	WorkspaceID core.WorkspaceID
	Clock       func() time.Time
	IDs         *core.IDGenerator
	Directory   AgentDirectory
	Delivery    Delivery
	Limits      Limits
}

// DelegationRequest asks the broker to create a child task. Requester must
// own the parent task. PermissionPolicy and Limits can only narrow inherited
// authority and resources.
type DelegationRequest struct {
	Requester           core.AgentID
	Target              core.AgentID
	ParentTaskID        core.TaskID
	Objective           string
	ContextRefs         []string
	ExpectedDeliverable string
	CESPolicy           *core.CESPolicy
	PermissionPolicy    *core.PermissionPolicy
	RequiresApproval    bool
	Limits              *core.TaskLimits
	Timeout             time.Duration
	CausationID         core.EventID
}

// ConsultationRequest asks for an opinion without assigning a task or
// transferring ownership.
type ConsultationRequest struct {
	Requester        core.AgentID
	Target           core.AgentID
	ParentTaskID     core.TaskID
	Question         string
	Evidence         []string
	References       []string
	PermissionPolicy *core.PermissionPolicy
	RequiresApproval bool
	Timeout          time.Duration
	CausationID      core.EventID
}

// ConsultationResult records findings without making them authoritative
// facts. The caller retains responsibility for how to use the result.
type ConsultationResult struct {
	Question    string   `json:"question"`
	Evidence    []string `json:"evidence,omitempty"`
	Findings    string   `json:"findings"`
	Uncertainty string   `json:"uncertainty"`
	References  []string `json:"references,omitempty"`
}

type ConsultationStatus string

const (
	ConsultationOpen      ConsultationStatus = "open"
	ConsultationCompleted ConsultationStatus = "completed"
	ConsultationFailed    ConsultationStatus = "failed"
	ConsultationTimedOut  ConsultationStatus = "timed_out"
	ConsultationCancelled ConsultationStatus = "cancelled"
)

type Consultation struct {
	ID               core.ConsultationID `json:"id"`
	RequestMessageID core.MessageID      `json:"request_message_id"`
	Requester        core.AgentID        `json:"requester"`
	Target           core.AgentID        `json:"target"`
	ParentTaskID     core.TaskID         `json:"parent_task_id,omitempty"`
	Question         string              `json:"question"`
	Evidence         []string            `json:"evidence,omitempty"`
	References       []string            `json:"references,omitempty"`
	Status           ConsultationStatus  `json:"status"`
	CreatedAt        time.Time           `json:"created_at"`
	Deadline         time.Time           `json:"deadline"`
	Result           *ConsultationResult `json:"result,omitempty"`
	FailureReason    string              `json:"failure_reason,omitempty"`
	CorrelationID    core.EventID        `json:"correlation_id,omitempty"`
	CausationID      core.EventID        `json:"causation_id,omitempty"`
}

// Snapshot returns detached broker state for projections.
type Snapshot struct {
	Messages      []core.Message `json:"messages"`
	Tasks         []core.Task    `json:"tasks"`
	Consultations []Consultation `json:"consultations"`
	Events        []core.Event   `json:"events"`
}

func (snapshot Snapshot) Task(id core.TaskID) (core.Task, bool) {
	for _, task := range snapshot.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return core.Task{}, false
}

func (snapshot Snapshot) Consultation(id core.ConsultationID) (Consultation, bool) {
	for _, consultation := range snapshot.Consultations {
		if consultation.ID == id {
			return consultation, true
		}
	}
	return Consultation{}, false
}
