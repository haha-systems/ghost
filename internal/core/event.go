package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const EventSchemaVersion = 1

type Category string

const (
	CategoryConversation Category = "conversation"
	CategoryTask         Category = "task"
	CategoryTool         Category = "tool"
	CategoryCoordination Category = "coordination"
	CategoryMemory       Category = "memory"
	CategoryDecision     Category = "decision"
)

type ActorKind string

const (
	ActorAgent    ActorKind = "agent"
	ActorOperator ActorKind = "operator"
	ActorProvider ActorKind = "provider"
	ActorSystem   ActorKind = "system"
)

type Actor struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

// Event is the versioned envelope used between the runtime, adapters, and UI.
type Event struct {
	ID            EventID         `json:"id"`
	SchemaVersion int             `json:"schema_version"`
	Time          time.Time       `json:"time"`
	Workspace     WorkspaceID     `json:"workspace"`
	Actor         Actor           `json:"actor"`
	Source        string          `json:"source"`
	Type          string          `json:"type"`
	Category      Category        `json:"category"`
	TaskID        TaskID          `json:"task_id,omitempty"`
	SessionID     SessionID       `json:"session_id,omitempty"`
	AgentID       AgentID         `json:"agent_id,omitempty"`
	ThreadID      ThreadID        `json:"thread_id,omitempty"`
	CorrelationID EventID         `json:"correlation_id,omitempty"`
	CausationID   EventID         `json:"causation_id,omitempty"`
	Seq           uint64          `json:"seq"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

// CategoryOf maps event type prefixes to their stable envelope categories.
func CategoryOf(eventType string) Category {
	switch {
	case hasPrefix(eventType, "message", "conversation"):
		return CategoryConversation
	case hasPrefix(eventType, "task"):
		return CategoryTask
	case hasPrefix(eventType, "tool"):
		return CategoryTool
	case hasPrefix(eventType, "coordination", "agent", "session", "runtime", "approval"):
		return CategoryCoordination
	case hasPrefix(eventType, "memory"):
		return CategoryMemory
	case hasPrefix(eventType, "decision", "qac", "jev"):
		return CategoryDecision
	default:
		return ""
	}
}

func hasPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix+".") {
			return true
		}
	}
	return false
}

func (e Event) Validate() error {
	if err := e.ID.Validate(); err != nil {
		return fmt.Errorf("event ID: %w", err)
	}
	if e.SchemaVersion != EventSchemaVersion {
		return fmt.Errorf("unsupported event schema version %d", e.SchemaVersion)
	}
	if e.Time.IsZero() {
		return fmt.Errorf("event time is required")
	}
	if err := e.Workspace.Validate(); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	switch e.Actor.Kind {
	case ActorAgent, ActorOperator, ActorProvider, ActorSystem:
	default:
		return fmt.Errorf("unknown actor kind %q", e.Actor.Kind)
	}
	if strings.TrimSpace(e.Actor.ID) == "" {
		return fmt.Errorf("actor ID is required")
	}
	if e.Actor.Kind == ActorAgent {
		if err := (AgentID(e.Actor.ID)).Validate(); err != nil {
			return fmt.Errorf("actor agent ID: %w", err)
		}
	}
	if strings.TrimSpace(e.Source) == "" || strings.TrimSpace(e.Type) == "" {
		return fmt.Errorf("event source and type are required")
	}
	category := CategoryOf(e.Type)
	if category == "" || e.Category != category {
		return fmt.Errorf("event type %q has category %q, not %q", e.Type, category, e.Category)
	}
	if e.TaskID != "" {
		if err := e.TaskID.Validate(); err != nil {
			return fmt.Errorf("task ID: %w", err)
		}
	}
	if e.SessionID != "" {
		if err := e.SessionID.Validate(); err != nil {
			return fmt.Errorf("session ID: %w", err)
		}
	}
	if e.AgentID != "" {
		if err := e.AgentID.Validate(); err != nil {
			return fmt.Errorf("agent ID: %w", err)
		}
	}
	if e.ThreadID != "" {
		if err := e.ThreadID.Validate(); err != nil {
			return fmt.Errorf("thread ID: %w", err)
		}
	}
	if e.CorrelationID != "" {
		if err := e.CorrelationID.Validate(); err != nil {
			return fmt.Errorf("correlation ID: %w", err)
		}
	}
	if e.CausationID != "" {
		if err := e.CausationID.Validate(); err != nil {
			return fmt.Errorf("causation ID: %w", err)
		}
	}
	if e.Seq == 0 {
		return fmt.Errorf("event sequence must be positive")
	}
	if len(e.Payload) > 0 && !json.Valid(e.Payload) {
		return fmt.Errorf("event payload is not valid JSON")
	}
	if e.StreamKey() == "" {
		return fmt.Errorf("event stream key is empty")
	}
	return nil
}

// StreamKey chooses task scope first, then session scope, then workspace scope.
func (e Event) StreamKey() string {
	workspace := string(e.Workspace)
	switch {
	case e.TaskID != "":
		return workspace + "/task/" + string(e.TaskID)
	case e.SessionID != "":
		return workspace + "/session/" + string(e.SessionID)
	case e.Workspace != "":
		return workspace + "/workspace"
	default:
		return ""
	}
}

// StreamKey returns the stream key for e.
func StreamKey(e Event) string { return e.StreamKey() }

// Sequencer assigns increasing sequence numbers independently for each stream.
type Sequencer struct {
	mu   sync.Mutex
	last map[string]uint64
}

func NewSequencer() *Sequencer { return &Sequencer{last: map[string]uint64{}} }

func (s *Sequencer) Assign(e Event) (Event, error) {
	if s == nil {
		return Event{}, fmt.Errorf("nil event sequencer")
	}
	stream := e.StreamKey()
	if stream == "" {
		return Event{}, fmt.Errorf("event stream key is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[string]uint64{}
	}
	e.Seq = s.last[stream] + 1
	if err := e.Validate(); err != nil {
		return Event{}, err
	}
	s.last[stream] = e.Seq
	return e, nil
}

// Restore sets a stream's last sequence after replaying durable events.
func (s *Sequencer) Restore(stream string, sequence uint64) error {
	if s == nil || strings.TrimSpace(stream) == "" || sequence == 0 {
		return fmt.Errorf("stream and positive sequence are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[string]uint64{}
	}
	if sequence < s.last[stream] {
		return fmt.Errorf("sequence for stream %q moved backwards", stream)
	}
	s.last[stream] = sequence
	return nil
}

func (s *Sequencer) Current(stream string) uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last[stream]
}
