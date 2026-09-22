package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

func mergeRateLimitMaps(previous, incoming map[string]any) map[string]any {
	result := cloneMap(previous)
	for key, value := range incoming {
		if nested, ok := value.(map[string]any); ok {
			prior, _ := result[key].(map[string]any)
			result[key] = mergeRateLimitMaps(prior, nested)
			continue
		}
		result[key] = value
	}
	return result
}

func cloneMap(values map[string]any) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		if nested, ok := value.(map[string]any); ok {
			result[key] = cloneMap(nested)
			continue
		}
		result[key] = value
	}
	return result
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
	if method == "account/rateLimits/updated" {
		if summary := rateLimitSummary(p); summary != "" {
			return summary
		}
	}

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

func rateLimitSummary(p map[string]any) string {
	limits, ok := rateLimitMap(p)
	if !ok {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, name := range []string{"primary", "secondary"} {
		window, ok := mapValue(limits, name).(map[string]any)
		if !ok {
			continue
		}
		used, ok := numberValue(window, "usedPercent", "used_percent")
		if !ok {
			continue
		}
		part := fmt.Sprintf("%s: %s%% remaining (%s%% used)", name, formatNumber(100-used), formatNumber(used))
		if duration, ok := numberValue(window, "windowDurationMins", "window_minutes"); ok {
			part += fmt.Sprintf(", %sm window", formatNumber(duration))
		}
		if reset, ok := numberValue(window, "resetsAt", "resets_at"); ok {
			part += ", resets " + time.Unix(int64(reset), 0).UTC().Format(time.RFC3339)
		}
		parts = append(parts, part)
	}
	if credits, ok := mapValue(limits, "credits").(map[string]any); ok {
		if balance, exists := stringValue(credits, "balance"); exists {
			parts = append(parts, "credits: balance "+balance)
		} else if unlimited, exists := boolValue(credits, "unlimited"); exists && unlimited {
			parts = append(parts, "credits: unlimited")
		}
	}
	return strings.Join(parts, "; ")
}

func rateLimitMetadata(p map[string]any) map[string]string {
	result := map[string]string{}
	limits, ok := rateLimitMap(p)
	if !ok {
		return result
	}
	for _, name := range []string{"primary", "secondary"} {
		window, ok := mapValue(limits, name).(map[string]any)
		if !ok {
			continue
		}
		if used, ok := numberValue(window, "usedPercent", "used_percent"); ok {
			result["rate_"+name+"_used_percent"] = formatNumber(used)
			result["rate_"+name+"_remaining_percent"] = formatNumber(100 - used)
		}
		if duration, ok := numberValue(window, "windowDurationMins", "window_minutes"); ok {
			result["rate_"+name+"_window_minutes"] = formatNumber(duration)
		}
		if reset, ok := numberValue(window, "resetsAt", "resets_at"); ok {
			result["rate_"+name+"_resets_at"] = strconv.FormatInt(int64(reset), 10)
		}
	}
	return result
}

func rateLimitMap(p map[string]any) (map[string]any, bool) {
	if limits, ok := mapValue(p, "rateLimits").(map[string]any); ok {
		return limits, true
	}
	if limits, ok := mapValue(p, "rate_limits").(map[string]any); ok {
		return limits, true
	}
	return nil, false
}

func mapValue(values map[string]any, key string) any {
	return values[key]
}

func numberValue(values map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		switch value := values[key].(type) {
		case float64:
			return value, true
		case int:
			return float64(value), true
		case int64:
			return float64(value), true
		}
	}
	return 0, false
}

func stringValue(values map[string]any, key string) (string, bool) {
	value, ok := values[key].(string)
	return value, ok
}

func boolValue(values map[string]any, key string) (bool, bool) {
	value, ok := values[key].(bool)
	return value, ok
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// samePresentation reports whether two message events belong to the same turn
// and may therefore be merged into one streamed response.
func samePresentation(a, b runtime.Event) bool {
	return a.Kind == runtime.KindMessage &&
		b.Kind == runtime.KindMessage &&
		a.AgentID == b.AgentID &&
		a.SessionID != "" &&
		a.SessionID == b.SessionID &&
		a.TurnID != "" &&
		a.TurnID == b.TurnID
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
