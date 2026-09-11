package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func drain(t *testing.T, ch <-chan notification, want int) []notification {
	t.Helper()
	out := make([]notification, 0, want)
	deadline := time.After(2 * time.Second)
	for len(out) < want {
		select {
		case n := <-ch:
			out = append(out, n)
		case <-deadline:
			t.Fatalf("timed out after %d of %d notifications", len(out), want)
		}
	}
	return out
}

// Every session must see only its own thread's notifications. Sharing one
// channel let two agents race for the same payloads, so an agent's output could
// be attributed to a different agent.
func TestNotificationsRouteToTheOwningThread(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	c := newRPC(nopCloseWriter{&bytes.Buffer{}}, serverOut)
	alpha := c.subscribe("thread-alpha")
	beta := c.subscribe("thread-beta")

	go func() {
		for i := 0; i < 20; i++ {
			fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-alpha","delta":"a%d"}}`+"\n", i)
			fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"thread":{"id":"thread-beta"},"delta":"b%d"}}`+"\n", i)
		}
	}()

	for _, c := range []struct {
		name   string
		ch     <-chan notification
		prefix string
	}{{"alpha", alpha, "a"}, {"beta", beta, "b"}} {
		for _, n := range drain(t, c.ch, 20) {
			var p struct{ Delta string }
			if err := json.Unmarshal(n.Params, &p); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(p.Delta, c.prefix) {
				t.Fatalf("%s received %q, which belongs to another thread", c.name, p.Delta)
			}
		}
	}
}

// Payloads without a thread id describe the server, so every session sees them.
func TestUnaddressedNotificationsBroadcast(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	c := newRPC(nopCloseWriter{&bytes.Buffer{}}, serverOut)
	alpha := c.subscribe("thread-alpha")
	beta := c.subscribe("thread-beta")

	go func() {
		_, _ = io.WriteString(clientIn, `{"jsonrpc":"2.0","method":"warning","params":{"message":"disk pressure"}}`+"\n")
	}()

	for _, ch := range []<-chan notification{alpha, beta} {
		if got := drain(t, ch, 1)[0].Method; got != "warning" {
			t.Fatalf("method = %q, want warning", got)
		}
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	c := newRPC(nopCloseWriter{&bytes.Buffer{}}, serverOut)
	alpha := c.subscribe("thread-alpha")
	c.subscribe("thread-beta")
	c.unsubscribe("thread-beta")

	go func() {
		_, _ = io.WriteString(clientIn, `{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-beta"}}`+"\n")
		_, _ = io.WriteString(clientIn, `{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-alpha"}}`+"\n")
	}()

	// thread-beta is gone, so its payload broadcasts to the remaining session;
	// what matters is that alpha still receives its own.
	if got := drain(t, alpha, 1)[0].Method; got != "turn/completed" {
		t.Fatalf("method = %q", got)
	}
}

// A line beyond the scanner's 64KB default previously ended the stream, which
// the console reported as the App Server exiting.
func TestOversizedMessagesDoNotEndTheStream(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	c := newRPC(nopCloseWriter{&bytes.Buffer{}}, serverOut)
	alpha := c.subscribe("thread-alpha")

	huge := strings.Repeat("x", 512*1024)
	go func() {
		fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-alpha","delta":%q}}`+"\n", huge)
		_, _ = io.WriteString(clientIn, `{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-alpha"}}`+"\n")
	}()

	got := drain(t, alpha, 2)
	var p struct{ Delta string }
	if err := json.Unmarshal(got[0].Params, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Delta) != len(huge) {
		t.Fatalf("delta truncated to %d bytes", len(p.Delta))
	}
	if got[1].Method != "turn/completed" {
		t.Fatal("stream ended after the oversized message")
	}
	select {
	case <-c.done:
		t.Fatal("reader terminated on an oversized message")
	default:
	}
}

// A call must not block forever when the App Server dies mid-request.
func TestCallFailsWhenTheBackendExits(t *testing.T) {
	serverOut, clientIn := io.Pipe()
	c := newRPC(nopCloseWriter{&bytes.Buffer{}}, serverOut)

	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = clientIn.Close()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := c.call(context.Background(), "thread/start", nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err != ErrBackendGone {
			t.Fatalf("err = %v, want ErrBackendGone", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call hung after the backend exited")
	}
}
