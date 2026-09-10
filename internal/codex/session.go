package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/haha-systems/ghost/internal/runtime"
	"strings"
	"sync"
	"time"
)

type Session struct {
	agentID, threadID string
	client            *rpcClient
	guard             *runtime.Guard
	events            chan runtime.Event
	mu                sync.Mutex
	closed            bool
}

func newSession(ctx context.Context, c *rpcClient, cfg runtime.SessionConfig) (*Session, error) {
	id, e := startThread(ctx, c, cfg)
	if e != nil {
		return nil, e
	}
	s := &Session{agentID: cfg.AgentID, threadID: id, client: c, guard: runtime.NewGuard(), events: make(chan runtime.Event, 64)}
	s.guard.Ready()
	go s.listen(ctx)
	s.emit(runtime.Event{Kind: runtime.KindSession, Summary: "session ready", AgentID: cfg.AgentID, SessionID: id})
	return s, nil
}
func (s *Session) ID() string                   { return s.threadID }
func (s *Session) State() runtime.SessionState  { return s.guard.State() }
func (s *Session) Events() <-chan runtime.Event { return s.events }
func (s *Session) Stats() runtime.SessionStats  { return s.guard.Stats() }
func (s *Session) Send(ctx context.Context, in runtime.Input) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	id := fmt.Sprintf("turn-%d", time.Now().UnixNano())
	if e := s.guard.StartTurn(id); e != nil {
		s.mu.Unlock()
		return e
	}
	s.mu.Unlock()
	b, e := s.client.call(ctx, "turn/start", map[string]any{"threadId": s.threadID, "input": []any{map[string]any{"type": "text", "text": in.Text}}})
	if e != nil {
		s.guard.Complete(id, true)
		return e
	}
	var reply struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(b, &reply) == nil && reply.Turn.ID != "" {
		s.guard.ReplaceTurn(id, reply.Turn.ID)
	}
	s.guard.Activity(int64(len(in.Text)), 0)
	s.emit(runtime.Event{Time: time.Now(), AgentID: s.agentID, SessionID: s.threadID, TurnID: s.guard.ActiveTurn(), Kind: runtime.KindStatus, Summary: "turn started"})
	return nil
}
func (s *Session) Steer(ctx context.Context, in runtime.Input) error {
	id := s.guard.ActiveTurn()
	if e := s.guard.CheckActive(id); e != nil {
		return e
	}
	_, e := s.client.call(ctx, "turn/steer", map[string]any{"threadId": s.threadID, "turnId": id, "input": []any{map[string]any{"type": "text", "text": in.Text}}})
	return e
}
func (s *Session) Interrupt(ctx context.Context) error {
	id := s.guard.ActiveTurn()
	if e := s.guard.BeginInterrupt(id); e != nil {
		return e
	}
	_, e := s.client.call(ctx, "turn/interrupt", map[string]any{"threadId": s.threadID, "turnId": id})
	s.emit(runtime.Event{Time: time.Now(), AgentID: s.agentID, SessionID: s.threadID, TurnID: id, Kind: runtime.KindStatus, Summary: "interrupt requested"})
	return e
}
func (s *Session) listen(ctx context.Context) {
	for {
		select {
		case n := <-s.client.notifications:
			s.handle(n)
		case <-ctx.Done():
			return
		case <-s.client.done:
			s.guard.Fail()
			s.emit(runtime.Event{Time: time.Now(), AgentID: s.agentID, SessionID: s.threadID, Kind: runtime.KindError, Summary: "Codex App Server exited"})
			return
		}
	}
}
func (s *Session) handle(n notification) {
	var p map[string]any
	_ = json.Unmarshal(n.Params, &p)
	kind := normalizeKind(n.Method)
	summary := n.Method
	if v, ok := p["delta"].(string); ok {
		kind = runtime.KindMessage
		summary = v
		s.guard.Activity(0, int64(len(v)))
	}
	if n.Method == "turn/completed" || n.Method == "turn/failed" {
		id, _ := p["turnId"].(string)
		s.guard.Complete(id, n.Method == "turn/failed")
	}
	s.emit(runtime.Event{Time: time.Now(), AgentID: s.agentID, SessionID: s.threadID, TurnID: s.guard.ActiveTurn(), Kind: kind, Summary: summary, Raw: n.Params})
}
func normalizeKind(method string) runtime.EventKind {
	m := strings.ToLower(method)
	switch {
	case strings.Contains(m, "reasoning"):
		return runtime.KindThinking
	case strings.Contains(m, "command"), strings.Contains(m, "exec"):
		return runtime.KindCommand
	case strings.Contains(m, "file"), strings.Contains(m, "read"):
		return runtime.KindFile
	case strings.Contains(m, "tool"):
		return runtime.KindTool
	case strings.Contains(m, "usage"):
		return runtime.KindUsage
	case strings.Contains(m, "error"), strings.Contains(m, "failed"):
		return runtime.KindError
	default:
		return runtime.KindStatus
	}
}
func (s *Session) emit(e runtime.Event) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	select {
	case s.events <- e:
	default:
	}
}
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.guard.Stop()
	close(s.events)
	return nil
}
