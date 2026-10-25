package fake

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

type Runtime struct {
	mu          sync.Mutex
	Sessions    map[string]*Session
	StartConfig []runtime.SessionConfig
	Fail        error
}

func NewRuntime() *Runtime      { return &Runtime{Sessions: map[string]*Session{}} }
func (r *Runtime) Name() string { return "fake" }
func (r *Runtime) Capabilities() runtime.Capabilities {
	return runtime.Capabilities{Steering: true, Interrupt: true, ToolEvents: true, ReasoningEvents: true}
}
func (r *Runtime) Start(_ context.Context, cfg runtime.SessionConfig) (runtime.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Fail != nil {
		return nil, r.Fail
	}
	r.StartConfig = append(r.StartConfig, cfg)
	s := NewSession(cfg.AgentID)
	r.Sessions[cfg.AgentID] = s
	return s, nil
}
func (r *Runtime) StartConfigs() []runtime.SessionConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtime.SessionConfig(nil), r.StartConfig...)
}
func (r *Runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.Sessions {
		_ = s.Close()
	}
	return nil
}

type Session struct {
	Agent                              string
	Guard                              *runtime.Guard
	Out                                chan runtime.Event
	mu                                 sync.Mutex
	closed                             bool
	dropped                            atomic.Int64
	LastSend, LastSteer, LastInterrupt string
	OnSend, OnSteer                    func(runtime.Input) []runtime.Event
}

func NewSession(agent string) *Session {
	g := runtime.NewGuard()
	g.Ready()
	return &Session{Agent: agent, Guard: g, Out: make(chan runtime.Event, 32)}
}
func (s *Session) ID() string                  { return s.Agent }
func (s *Session) State() runtime.SessionState { return s.Guard.State() }
func (s *Session) LastSteeredText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastSteer
}
func (s *Session) LastSentText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastSend
}
func (s *Session) LastInterruptedTurn() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastInterrupt
}
func (s *Session) Events() <-chan runtime.Event { return s.Out }
func (s *Session) Stats() runtime.SessionStats  { return s.Guard.Stats() }
func (s *Session) Metadata() runtime.SessionMetadata {
	return runtime.SessionMetadata{ThreadID: s.Agent, Model: "fake"}
}
func (s *Session) Send(_ context.Context, in runtime.Input) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	id := "fake-turn"
	if e := s.Guard.StartTurn(id); e != nil {
		s.mu.Unlock()
		return e
	}
	s.LastSend = in.Text
	respond := s.OnSend
	s.mu.Unlock()
	s.respond(in, respond)
	return nil
}
func (s *Session) Steer(_ context.Context, in runtime.Input) error {
	s.mu.Lock()
	if e := s.Guard.CheckActive(s.Guard.ActiveTurn()); e != nil {
		s.mu.Unlock()
		return e
	}
	s.LastSteer = in.Text
	respond := s.OnSteer
	s.mu.Unlock()
	s.respond(in, respond)
	return nil
}

func (s *Session) respond(input runtime.Input, respond func(runtime.Input) []runtime.Event) {
	if respond == nil {
		return
	}
	for _, item := range respond(input) {
		if item.Time.IsZero() {
			item.Time = time.Now()
		}
		if item.AgentID == "" {
			item.AgentID = s.Agent
		}
		if item.SessionID == "" {
			item.SessionID = s.ID()
		}
		if item.TurnID == "" {
			item.TurnID = s.Guard.ActiveTurn()
		}
		if item.Metadata["backend_method"] == "turn/completed" {
			s.Guard.Complete(item.TurnID, false)
		} else if item.Metadata["backend_method"] == "turn/failed" {
			s.Guard.Complete(item.TurnID, true)
		}
		s.Emit(item)
	}
}
func (s *Session) Interrupt(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.Guard.ActiveTurn()
	if e := s.Guard.BeginInterrupt(id); e != nil {
		return e
	}
	s.LastInterrupt = id
	s.Guard.Complete(id, false)
	return nil
}
func (s *Session) Emit(e runtime.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		select {
		case s.Out <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

func (s *Session) Dropped() int64 { return s.dropped.Load() }
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.Guard.Stop()
		close(s.Out)
	}
	return nil
}
