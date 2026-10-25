// Package event defines user-facing events shared by Ghost views.
package event

import "time"

type Kind string

const (
	KindSystem     Kind = "system"
	KindAgent      Kind = "agent"
	KindSteering   Kind = "steering"
	KindStatus     Kind = "status"
	KindError      Kind = "error"
	KindResponse   Kind = "response"
	KindQAC        Kind = "qac"
	KindCES        Kind = "ces"
	KindPhase      Kind = "phase"
	KindHypothesis Kind = "hypothesis"
	KindReject     Kind = "reject"
	KindFrame      Kind = "frame"
	KindAction     Kind = "action"
	KindVerify     Kind = "verify"
	KindContradict Kind = "contradict"
	KindReopen     Kind = "reopen"
	KindComplete   Kind = "complete"
	KindIncomplete Kind = "incomplete"

	// These mirror the runtime's normalized event kinds. Without them the
	// backend's classification collapses to KindAgent before it reaches a view.
	KindThinking Kind = "thinking"
	KindCommand  Kind = "command"
	KindFile     Kind = "file"
	KindTool     Kind = "tool"
	KindUsage    Kind = "usage"
	KindSession  Kind = "session"
)

type Event struct {
	Time      time.Time
	Source    string
	Kind      Kind
	Message   string
	SessionID string
	TurnID    string
	Metadata  map[string]string
	Raw       []byte
}

// MergeMeta returns a new map with extra overlaid on base. Neither input is modified.
func MergeMeta(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}
