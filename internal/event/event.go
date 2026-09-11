// Package event defines user-facing events shared by Ghost views.
package event

import "time"

type Kind string

const (
	KindSystem   Kind = "system"
	KindAgent    Kind = "agent"
	KindSteering Kind = "steering"
	KindStatus   Kind = "status"
	KindError    Kind = "error"
	KindResponse Kind = "response"
	KindQAC      Kind = "qac"

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
	Raw       []byte
}
