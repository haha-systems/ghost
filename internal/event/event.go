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
