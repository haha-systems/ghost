package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Capabilities struct{ Steering, Interrupt, Resume, Usage, ToolEvents, ReasoningEvents bool }
type SessionState string

const (
	StateStarting     SessionState = "starting"
	StateIdle         SessionState = "idle"
	StateRunning      SessionState = "running"
	StateInterrupting SessionState = "interrupting"
	StateFailed       SessionState = "failed"
	StateStopped      SessionState = "stopped"
)

type SessionConfig struct{ AgentID, WorkingDir, Model string }
type Input struct{ Text string }
type EventKind string

const (
	KindSession  EventKind = "session"
	KindThinking EventKind = "thinking"
	KindMessage  EventKind = "message"
	KindCommand  EventKind = "command"
	KindFile     EventKind = "file"
	KindTool     EventKind = "tool"
	KindUsage    EventKind = "usage"
	KindStatus   EventKind = "status"
	KindError    EventKind = "error"
)

type Event struct {
	Time                       time.Time
	AgentID, SessionID, TurnID string
	Kind                       EventKind
	Summary, Detail            string
	Metadata                   map[string]string
	Raw                        []byte
}
type SessionStats struct {
	StartedAt, LastActivity time.Time
	Turns, CompletedTurns   int
	InputBytes, OutputBytes int64
}
type SessionMetadata struct{ ThreadID, Model string }
type Runtime interface {
	Name() string
	Capabilities() Capabilities
	Start(context.Context, SessionConfig) (Session, error)
	Close() error
}
type Session interface {
	ID() string
	State() SessionState
	Send(context.Context, Input) error
	Steer(context.Context, Input) error
	Interrupt(context.Context) error
	Events() <-chan Event
	Stats() SessionStats
	Metadata() SessionMetadata
	Close() error
}

var ErrTurnActive = errors.New("session turn already active")
var ErrNoActiveTurn = errors.New("session has no active turn")

type Guard struct {
	mu         sync.Mutex
	state      SessionState
	activeTurn string
	stats      SessionStats
}

func NewGuard() *Guard {
	return &Guard{state: StateStarting, stats: SessionStats{StartedAt: time.Now()}}
}
func (g *Guard) State() SessionState { g.mu.Lock(); defer g.mu.Unlock(); return g.state }
func (g *Guard) Ready() {
	g.mu.Lock()
	if g.state == StateStarting {
		g.state = StateIdle
	}
	g.mu.Unlock()
}

// Recover returns a failed session to idle so a single failed turn does not
// permanently silence the agent. Stopped sessions stay stopped.
func (g *Guard) Recover() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != StateFailed {
		return false
	}
	g.state = StateIdle
	g.activeTurn = ""
	return true
}

func (g *Guard) StartTurn(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != StateIdle {
		return ErrTurnActive
	}
	g.activeTurn = id
	g.state = StateRunning
	g.stats.Turns++
	return nil
}
func (g *Guard) ActiveTurn() string { g.mu.Lock(); defer g.mu.Unlock(); return g.activeTurn }
func (g *Guard) ReplaceTurn(from, to string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeTurn != from || to == "" {
		return false
	}
	g.activeTurn = to
	return true
}
func (g *Guard) BeginInterrupt(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != StateRunning || g.activeTurn != id {
		return ErrNoActiveTurn
	}
	g.state = StateInterrupting
	return nil
}
func (g *Guard) CheckActive(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != StateRunning || g.activeTurn != id {
		return ErrNoActiveTurn
	}
	return nil
}
func (g *Guard) Complete(id string, failed bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeTurn != id {
		return false
	}
	g.activeTurn = ""
	if failed {
		g.state = StateFailed
	} else {
		g.state = StateIdle
		g.stats.CompletedTurns++
	}
	return true
}
func (g *Guard) Abort(id string) bool { return g.Complete(id, true) }
func (g *Guard) Fail()                { g.mu.Lock(); g.state = StateFailed; g.mu.Unlock() }
func (g *Guard) Stop()                { g.mu.Lock(); g.state = StateStopped; g.mu.Unlock() }
func (g *Guard) Stats() SessionStats  { g.mu.Lock(); defer g.mu.Unlock(); return g.stats }
func (g *Guard) Activity(in, out int64) {
	g.mu.Lock()
	g.stats.LastActivity = time.Now()
	g.stats.InputBytes += in
	g.stats.OutputBytes += out
	g.mu.Unlock()
}
