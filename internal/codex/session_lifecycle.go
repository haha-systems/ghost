package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

func (s *Session) Send(ctx context.Context, in runtime.Input) error {
	s.callMu.Lock()
	defer s.callMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	s.guard.Recover()
	id := fmt.Sprintf("turn-%d", time.Now().UnixNano())
	if e := s.guard.StartTurn(id); e != nil {
		s.mu.Unlock()
		return e
	}
	s.mu.Unlock()

	params := map[string]any{"threadId": s.threadID, "input": []any{map[string]any{"type": "text", "text": in.Text}}}
	if in.OutputSchema != nil {
		params["outputSchema"] = in.OutputSchema
	}
	b, e := s.client.call(ctx, "turn/start", params)
	if e != nil {
		active := s.guard.ActiveTurn()
		if active == "" {
			active = id
		}
		s.guard.Complete(active, true)
		return e
	}
	var reply struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	backendTurnID := ""
	if json.Unmarshal(b, &reply) == nil && reply.Turn.ID != "" {
		s.guard.ReplaceTurn(id, reply.Turn.ID)
		backendTurnID = reply.Turn.ID
	}
	s.guard.Activity(int64(len(in.Text)), 0)
	s.emit(runtime.Event{Time: time.Now(), AgentID: s.agentID, SessionID: s.threadID, TurnID: backendTurnID, Kind: runtime.KindStatus, Summary: "turn started", Metadata: map[string]string{"backend_method": "turn/started"}})
	return nil
}

func (s *Session) Steer(ctx context.Context, in runtime.Input) error {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	id := s.guard.ActiveTurn()
	if e := s.guard.CheckActive(id); e != nil {
		return e
	}
	_, e := s.client.call(ctx, "turn/steer", map[string]any{"threadId": s.threadID, "expectedTurnId": id, "input": []any{map[string]any{"type": "text", "text": in.Text}}})
	return e
}

func (s *Session) Interrupt(ctx context.Context) error {
	s.callMu.Lock()
	defer s.callMu.Unlock()
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

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		done := s.listenDone
		s.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	s.closed = true
	cancel := s.cancel
	done := s.listenDone
	s.mu.Unlock()
	s.callMu.Lock()
	defer s.callMu.Unlock()

	if s.client != nil {
		s.client.unsubscribe(s.threadID)
	}
	s.guard.Stop()
	if s.client != nil {
		select {
		case <-s.client.done:
		default:
			ctx, cancelUnsubscribe := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = s.client.call(ctx, "thread/unsubscribe", map[string]any{"threadId": s.threadID})
			cancelUnsubscribe()
		}
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	close(s.events)
	return nil
}
