// Package history owns Ghost's canonical run history.
//
// History belongs to the run, not to a screen. Before this package the agent
// detail model was the only home for an agent's log, so work performed while
// its screen was closed was never recorded anywhere a view could recover it.
// The store below is updated as events arrive and every view projects from it.
package history

import (
	"time"

	"github.com/haha-systems/ghost/internal/event"
)

// Metadata keys carried by QAC entries. A QAC event's source is the coordinator
// rather than an agent, so the agents it concerns are named explicitly.
const (
	MetaFrom = "from"
	MetaTo   = "to"
	MetaWork = "work"
)

// Entry is one piece of run evidence. It deliberately keeps the session, turn,
// kind, metadata, and raw payload that a later semantic phase needs to project
// Activity, QAC, and Raw views without reworking storage.
type Entry struct {
	Time time.Time

	AgentID   string
	SessionID string
	TurnID    string

	Kind event.Kind

	Message string

	Metadata map[string]string
	Raw      []byte
}

// Meta reads one metadata value, tolerating a nil map.
func (e Entry) Meta(key string) string {
	if e.Metadata == nil {
		return ""
	}
	return e.Metadata[key]
}

// clone copies an entry deeply enough that a caller cannot reach back into the
// store through its metadata map or raw payload.
func (e Entry) clone() Entry {
	out := e
	if e.Metadata != nil {
		out.Metadata = make(map[string]string, len(e.Metadata))
		for k, v := range e.Metadata {
			out.Metadata[k] = v
		}
	}
	if e.Raw != nil {
		out.Raw = append([]byte(nil), e.Raw...)
	}
	return out
}

// Belongs reports whether an entry is part of one agent's history.
//
// The base rule is strict ownership by AgentID, which keeps one agent's work
// out of another's pane. QAC entries are the single exception: a handoff is
// evidence for both the agent that gave up the work and the one that received
// it, so it is projected into both.
func Belongs(e Entry, agentID string) bool {
	if agentID == "" {
		return false
	}
	if e.AgentID == agentID {
		return true
	}
	if e.Kind != event.KindQAC {
		return false
	}
	return e.Meta(MetaFrom) == agentID || e.Meta(MetaTo) == agentID
}
