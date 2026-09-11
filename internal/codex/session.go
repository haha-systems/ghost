package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/haha-systems/ghost/internal/runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Session struct {
	agentID, threadID, model string
	client                   *rpcClient
	guard                    *runtime.Guard
	events                   chan runtime.Event
	inbox                    <-chan notification
	dropped                  atomic.Int64
	mu                       sync.Mutex
	closed                   bool
}

func newSession(ctx context.Context, c *rpcClient, cfg runtime.SessionConfig) (*Session, error) {
	thread, e := startThread(ctx, c, cfg)
	if e != nil {
		return nil, e
	}
	s := &Session{agentID: cfg.AgentID, threadID: thread.ID, model: thread.Model, client: c, guard: runtime.NewGuard(), events: make(chan runtime.Event, 256)}
	s.inbox = c.subscribe(thread.ID)
	s.guard.Ready()
	go s.listen(ctx)
	s.emit(runtime.Event{Kind: runtime.KindSession, Summary: "session ready", AgentID: cfg.AgentID, SessionID: thread.ID})
	return s, nil
}
func (s *Session) ID() string                   { return s.threadID }
func (s *Session) State() runtime.SessionState  { return s.guard.State() }
func (s *Session) Events() <-chan runtime.Event { return s.events }
func (s *Session) Stats() runtime.SessionStats  { return s.guard.Stats() }
func (s *Session) Metadata() runtime.SessionMetadata {
	return runtime.SessionMetadata{ThreadID: s.threadID, Model: s.model}
}
func (s *Session) Send(ctx context.Context, in runtime.Input) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	// A previously failed turn must not silence the agent for the rest of the
	// run; an operator sending new input is an explicit request to resume.
	s.guard.Recover()
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
	_, e := s.client.call(ctx, "turn/steer", map[string]any{"threadId": s.threadID, "expectedTurnId": id, "input": []any{map[string]any{"type": "text", "text": in.Text}}})
	return e
}
func (s *Session) Interrupt(ctx context.Context) error {
	id := s.guard.ActiveTurn()
	if e := s.guard.BeginInterrupt(id); e != nil {
		return e
	}
	_, e := s.client.call(ctx, "turn/interrupt", map[string]any{"threadId": s.threadID, "turnId": id})
	if e != nil {
		s.guard.Abort(id)
		return e
	}
	s.emit(runtime.Event{Time: time.Now(), AgentID: s.agentID, SessionID: s.threadID, TurnID: id, Kind: runtime.KindStatus, Summary: "interrupt requested"})
	return nil
}
func (s *Session) listen(ctx context.Context) {
	for {
		select {
		case n := <-s.inbox:
			s.handle(n)
		case <-ctx.Done():
			s.guard.Stop()
			s.emit(runtime.Event{Time: time.Now(), AgentID: s.agentID, SessionID: s.threadID, Kind: runtime.KindSession, Summary: "session ended"})
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
	id := s.guard.ActiveTurn()
	if terminal := n.Method == "turn/completed" || n.Method == "turn/failed"; terminal {
		id = turnID(p)
		s.guard.Complete(id, n.Method == "turn/failed")
	}
	e := normalizeNotification(n, s.agentID, s.threadID, id)
	if e.Kind == runtime.KindMessage {
		if v, ok := p["delta"].(string); ok {
			s.guard.Activity(0, int64(len(v)))
		}
	}
	s.emit(e)
}

func normalizeNotification(n notification, agentID, sessionID, turnIDValue string) runtime.Event {
	var p map[string]any
	_ = json.Unmarshal(n.Params, &p)
	if id := turnID(p); id != "" {
		turnIDValue = id
	}
	return runtime.Event{Time: time.Now(), AgentID: agentID, SessionID: sessionID, TurnID: turnIDValue, Kind: normalizeKindFromPayload(n.Method, p), Summary: notificationSummary(n.Method, p), Metadata: map[string]string{"backend_method": n.Method}, Raw: n.Params}
}

func normalizeKindFromPayload(method string, p map[string]any) runtime.EventKind {
	if item, ok := p["item"].(map[string]any); ok {
		switch item["type"] {
		case "agentMessage":
			return runtime.KindMessage
		case "reasoning":
			return runtime.KindThinking
		case "commandExecution":
			return runtime.KindCommand
		case "fileChange", "fileRead":
			return runtime.KindFile
		case "toolCall", "mcpToolCall":
			return runtime.KindTool
		}
	}
	return normalizeKind(method)
}
func turnID(p map[string]any) string {
	if id, ok := p["turnId"].(string); ok {
		return id
	}
	if turn, ok := p["turn"].(map[string]any); ok {
		if id, ok := turn["id"].(string); ok {
			return id
		}
	}
	return ""
}
func notificationSummary(method string, p map[string]any) string {
	if d, ok := p["delta"].(string); ok {
		return d
	}
	if method == "turn/completed" {
		return "turn completed"
	}
	if method == "turn/failed" {
		return "turn failed"
	}
	if item, ok := p["item"].(map[string]any); ok {
		typ, _ := item["type"].(string)
		if text, ok := item["text"].(string); ok && text != "" {
			return text
		}
		switch typ {
		case "commandExecution":
			if v, ok := item["command"].(string); ok {
				if status, ok := item["status"].(string); ok && status != "" {
					return v + " (" + status + ")"
				}
				return v
			}
		case "agentMessage":
			if content, ok := item["content"].([]any); ok {
				for _, v := range content {
					if x, ok := v.(map[string]any); ok {
						if t, ok := x["text"].(string); ok {
							return t
						}
					}
				}
			}
		case "reasoning":
			return "reasoning"
		default:
			if typ != "" {
				return typ
			}
		}
	}
	for _, key := range []string{"message", "error", "status"} {
		if v, ok := p[key].(string); ok && v != "" {
			return v
		}
	}
	return method
}

// samePresentation reports whether two message events belong to the same turn
// and may therefore be merged into one streamed response.
func samePresentation(a, b runtime.Event) bool {
	return a.Kind == runtime.KindMessage && b.Kind == runtime.KindMessage && a.AgentID == b.AgentID && a.SessionID != "" && a.SessionID == b.SessionID && a.TurnID != "" && a.TurnID == b.TurnID
}

func normalizeKind(method string) runtime.EventKind {
	m := strings.ToLower(method)
	switch {
	case strings.Contains(m, "agentmessage"):
		return runtime.KindMessage
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
	// Report a backlog once the consumer catches up, so dropped agent output is
	// visible rather than silently discarded.
	if n := s.dropped.Load(); n > 0 {
		notice := runtime.Event{Time: time.Now(), AgentID: s.agentID, SessionID: s.threadID, Kind: runtime.KindError, Summary: fmt.Sprintf("%d events dropped; the console fell behind", n)}
		select {
		case s.events <- notice:
			s.dropped.Add(-n)
		default:
			s.dropped.Add(1)
			return
		}
	}
	select {
	case s.events <- e:
	default:
		s.dropped.Add(1)
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
	s.client.unsubscribe(s.threadID)
	s.guard.Stop()
	close(s.events)
	return nil
}
