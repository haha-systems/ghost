package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

func TestStartThreadSendsDeveloperInstructions(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	serverIn, clientOut := io.Pipe()
	c := newRPC(clientOut, serverOut)

	request := make(chan struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}, 1)
	go func() {
		var got struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(serverIn).Decode(&got)
		request <- struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}{got.Method, got.Params}
		_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{"thread":{"id":"thread-1"}}}`+"\n", got.ID)
	}()

	_, err := startThread(t.Context(), c, runtime.SessionConfig{
		AgentID:      "veil",
		WorkingDir:   "/work",
		Instructions: "global instructions\n\nagent soul",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := <-request
	if got.Method != "thread/start" {
		t.Fatalf("method = %q, want thread/start", got.Method)
	}
	if got.Params["developerInstructions"] != "global instructions\n\nagent soul" {
		t.Fatalf("developerInstructions = %#v", got.Params["developerInstructions"])
	}
	if _, exists := got.Params["baseInstructions"]; exists {
		t.Fatal("instructions sent through baseInstructions")
	}
}

func TestRuntimeProbesCodexBeforeStarting(t *testing.T) {
	old := codexLookPath
	defer func() { codexLookPath = old }()
	calls := 0
	var binary string
	codexLookPath = func(name string) (string, error) {
		calls++
		binary = name
		return "", errors.New("missing")
	}
	r := NewRuntime()
	_, err := r.Start(context.Background(), runtime.SessionConfig{AgentID: "one"})
	if err == nil || !strings.Contains(err.Error(), "codex") || !strings.Contains(err.Error(), "install") {
		t.Fatalf("error = %v, want codex install guidance", err)
	}
	_, _ = r.Start(context.Background(), runtime.SessionConfig{AgentID: "two"})
	if calls != 1 || binary != "codex" {
		t.Fatalf("codex probe = (%d, %q), want (1, %q)", calls, binary, "codex")
	}
}

type nopCloseWriter struct{ io.Writer }

func (nopCloseWriter) Close() error { return nil }

func TestRPCCorrelatesResponsesAcrossNotifications(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	c := newRPC(nopCloseWriter{&bytes.Buffer{}}, serverOut)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = clientIn.Write([]byte(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"delta":"hi"}}
{"jsonrpc":"2.0","id":1,"result":{"ok":true}}

`))
	}()
	got, err := c.call(ctx, "initialize", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("result=%s", got)
	}
	select {
	case n := <-c.notifications:
		if n.Method != "item/agentMessage/delta" {
			t.Fatalf("notification=%s", n.Method)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// A local placeholder is not a backend turn ID. If turn/start does not return
// an ID, exposing the placeholder can make consumers reject the real turn.
func TestSendDoesNotExposeLocalTurnPlaceholder(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	serverIn, clientOut := io.Pipe()
	c := newRPC(clientOut, serverOut)
	g := runtime.NewGuard()
	g.Ready()
	s := &Session{agentID: "wraith", threadID: "thread-1", client: c, guard: g, events: make(chan runtime.Event, 1)}

	go func() {
		var request struct {
			ID int64 `json:"id"`
		}
		_ = json.NewDecoder(serverIn).Decode(&request)
		_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{}}`+"\n", request.ID)
	}()

	if err := s.Send(t.Context(), runtime.Input{Text: "triage"}); err != nil {
		t.Fatal(err)
	}
	event := <-s.events
	if event.TurnID != "" {
		t.Fatalf("turn id=%q, want empty until the backend establishes it", event.TurnID)
	}
}
