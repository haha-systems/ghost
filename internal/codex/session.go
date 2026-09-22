package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

type Session struct {
	agentID, threadID, model string
	client                   *rpcClient
	guard                    *runtime.Guard
	events                   chan runtime.Event
	inbox                    <-chan notification
	cancel                   context.CancelFunc
	listenDone               chan struct{}
	dropped                  atomic.Int64
	mu                       sync.Mutex
	callMu                   sync.Mutex
	closed                   bool
	rateMu                   sync.Mutex
	rateLimits               map[string]any
}

func newSession(ctx context.Context, c *rpcClient, cfg runtime.SessionConfig) (*Session, error) {
	return newSessionWithListener(ctx, ctx, c, cfg)
}

func newSessionWithListener(startCtx, listenerCtx context.Context, c *rpcClient, cfg runtime.SessionConfig) (*Session, error) {
	thread, e := startThread(startCtx, c, cfg)
	if e != nil {
		return nil, e
	}
	sessionCtx, cancel := context.WithCancel(listenerCtx)
	s := &Session{agentID: cfg.AgentID, threadID: thread.ID, model: thread.Model, client: c, guard: runtime.NewGuard(), events: make(chan runtime.Event, 256), cancel: cancel, listenDone: make(chan struct{})}
	s.inbox = c.subscribe(thread.ID)
	s.guard.Ready()
	go func() {
		defer close(s.listenDone)
		s.listen(sessionCtx)
	}()
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

	if n.Method == "turn/started" {
		if backendTurnID := turnID(p); backendTurnID != "" {
			s.guard.ReplaceTurn(s.guard.ActiveTurn(), backendTurnID)
		}
	}
	id := s.guard.ActiveTurn()

	if terminal := n.Method == "turn/completed" || n.Method == "turn/failed"; terminal {
		id = turnID(p)
		s.guard.Complete(id, n.Method == "turn/failed")
	}

	if n.Method == "account/rateLimits/updated" {
		p = s.mergeRateLimits(p)
	}
	e := normalizeNotificationPayload(n, s.agentID, s.threadID, id, p)

	if e.Kind == runtime.KindMessage {
		if v, ok := p["delta"].(string); ok {
			s.guard.Activity(0, int64(len(v)))
		}
	}

	s.emit(e)
}

func (s *Session) mergeRateLimits(p map[string]any) map[string]any {
	incoming, ok := rateLimitMap(p)
	if !ok {
		return p
	}
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	s.rateLimits = mergeRateLimitMaps(s.rateLimits, incoming)
	merged := cloneMap(p)
	merged["rateLimits"] = cloneMap(s.rateLimits)
	return merged
}

func (s *Session) emit(e runtime.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	// Report a backlog once the consumer catches up, so dropped agent output is
	// visible rather than silently discarded.
	if n := s.dropped.Load(); n > 0 {
		notice := runtime.Event{
			Time:      time.Now(),
			AgentID:   s.agentID,
			SessionID: s.threadID,
			Kind:      runtime.KindError,
			Summary:   fmt.Sprintf("%d events dropped; the console fell behind", n),
		}

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
