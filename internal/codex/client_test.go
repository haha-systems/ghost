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
		Sandbox:      runtime.SandboxEnabled,
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
	if got.Params["sandbox"] != "workspace-write" {
		t.Fatalf("sandbox = %#v, want workspace-write", got.Params["sandbox"])
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

func TestSessionCloseUnsubscribesThread(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	serverIn, clientOut := io.Pipe()
	c := newRPC(clientOut, serverOut)
	defer func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
	}()

	requests := make(chan string, 2)
	go func() {
		decoder := json.NewDecoder(serverIn)
		for {
			var request struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			requests <- request.Method
			switch request.Method {
			case "thread/start":
				_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{"thread":{"id":"thread-1"}}}`+"\n", request.ID)
			case "thread/unsubscribe":
				_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{"status":"unsubscribed"}}`+"\n", request.ID)
			}
		}
	}()

	s, err := newSession(t.Context(), c, runtime.SessionConfig{AgentID: "phase"})
	if err != nil {
		t.Fatal(err)
	}
	closeErrs := make(chan error, 2)
	go func() { closeErrs <- s.Close() }()
	go func() { closeErrs <- s.Close() }()
	for range 2 {
		if err := <-closeErrs; err != nil {
			t.Fatal(err)
		}
	}

	select {
	case method := <-requests:
		if method != "thread/start" {
			t.Fatalf("startup request = %q, want thread/start", method)
		}
	case <-time.After(time.Second):
		t.Fatal("session startup request was not observed")
	}
	select {
	case method := <-requests:
		if method != "thread/unsubscribe" {
			t.Fatalf("close request = %q, want thread/unsubscribe", method)
		}
	case <-time.After(time.Second):
		t.Fatal("session close did not unsubscribe the backend thread")
	}
}

func TestRepeatedSessionLifecycleDoesNotRetainResources(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	serverIn, clientOut := io.Pipe()
	c := newRPC(clientOut, serverOut)
	defer func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
	}()

	requests := make(chan string, 128)
	go func() {
		decoder := json.NewDecoder(serverIn)
		thread := 0
		for {
			var request struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			requests <- request.Method
			switch request.Method {
			case "thread/start":
				thread++
				_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{"thread":{"id":"thread-%d"}}}`+"\n", request.ID, thread)
			case "thread/unsubscribe":
				_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{"status":"unsubscribed"}}`+"\n", request.ID)
			}
		}
	}()

	for i := 0; i < 32; i++ {
		ctx, cancel := context.WithCancel(t.Context())
		s, err := newSession(ctx, c, runtime.SessionConfig{AgentID: "phase"})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if i%2 == 0 {
			cancel()
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, ok := <-s.listenDone; ok {
			t.Fatal("session listener remained active after Close")
		}
		c.mu.Lock()
		routes, pending := len(c.routes), len(c.pending)
		c.mu.Unlock()
		if routes != 0 || pending != 0 {
			t.Fatalf("iteration %d retained routes=%d pending=%d", i, routes, pending)
		}
		cancel()
	}
}

func TestSessionCloseAfterBackendExit(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	serverIn, clientOut := io.Pipe()
	c := newRPC(clientOut, serverOut)
	defer func() {
		_ = clientOut.Close()
		_ = serverIn.Close()
	}()

	go func() {
		var request struct {
			ID int64 `json:"id"`
		}
		if json.NewDecoder(serverIn).Decode(&request) == nil {
			_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{"thread":{"id":"thread-1"}}}`+"\n", request.ID)
		}
	}()
	s, err := newSession(t.Context(), c, runtime.SessionConfig{AgentID: "phase"})
	if err != nil {
		t.Fatal(err)
	}
	_ = clientIn.Close()
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("RPC reader did not observe backend exit")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-s.events:
		if ok {
			for range s.events {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("session event stream did not close after backend exit")
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

func TestRPCDeclinesServerApprovalRequestWithoutConsumingResponse(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	serverIn, clientOut := io.Pipe()
	c := newRPC(clientOut, serverOut)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		var outgoing map[string]any
		_ = json.NewDecoder(serverIn).Decode(&outgoing)
		_, _ = clientIn.Write([]byte(`{"jsonrpc":"2.0","id":"approval-1","method":"item/fileChange/requestApproval","params":{"threadId":"thread-1"}}` + "\n"))
		var approval map[string]any
		_ = json.NewDecoder(serverIn).Decode(&approval)
		if approval["id"] != "approval-1" {
			t.Errorf("approval response id = %#v", approval["id"])
		}
		result, _ := approval["result"].(map[string]any)
		if result["decision"] != "decline" {
			t.Errorf("approval decision = %#v", result["decision"])
		}
		_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%v,"result":{"ok":true}}`+"\n", outgoing["id"])
	}()

	got, err := c.call(ctx, "initialize", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("result=%s", got)
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
			ID     int64          `json:"id"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(serverIn).Decode(&request)
		if _, ok := request.Params["outputSchema"]; !ok {
			t.Error("turn/start omitted outputSchema")
		}
		_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{}}`+"\n", request.ID)
	}()

	if err := s.Send(t.Context(), runtime.Input{Text: "triage", OutputSchema: map[string]any{"type": "object"}}); err != nil {
		t.Fatal(err)
	}
	event := <-s.events
	if event.TurnID != "" {
		t.Fatalf("turn id=%q, want empty until the backend establishes it", event.TurnID)
	}
}
