package codex

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

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
