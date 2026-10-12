package codex

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

func TestMalformedNotificationProducesError(t *testing.T) {
	n := notification{Method: "turn/completed", Params: json.RawMessage(`{"turn":`)}
	e := normalizeNotification(n, "agent", "session", "turn")
	if e.Kind != runtime.KindError || !strings.Contains(e.Summary, "decode Codex notification") {
		t.Fatalf("event = %#v", e)
	}
	guard := runtime.NewGuard()
	guard.Ready()
	s := &Session{agentID: "agent", threadID: "session", guard: guard, events: make(chan runtime.Event, 1)}
	s.handle(n)
	select {
	case e := <-s.events:
		if e.Kind != runtime.KindError {
			t.Fatalf("event = %#v", e)
		}
	default:
		t.Fatal("session did not report malformed notification")
	}
}

type failedReplyWriter struct{}

func (failedReplyWriter) Write([]byte) (int, error) { return 0, errors.New("reply write failed") }
func (failedReplyWriter) Close() error              { return nil }

func TestFailedReplyStopsConnection(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	c := newRPC(failedReplyWriter{}, reader)
	c.writeReply(map[string]any{"id": 1, "result": nil})
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("failed reply left connection active")
	}
	if _, err := c.call(t.Context(), "test", nil); err == nil {
		t.Fatal("call succeeded after failed reply")
	}
}
