package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// maxMessageBytes bounds a single JSON-RPC line read from the App Server. The
// scanner default of 64KB is routinely exceeded by file reads and long agent
// messages, and an overrun ends the stream as though the process had died.
const maxMessageBytes = 16 * 1024 * 1024

// ErrBackendGone reports that the App Server exited while a call was in flight.
var ErrBackendGone = errors.New("codex app-server exited")

type process interface {
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.ReadCloser, error)
	StderrPipe() (io.ReadCloser, error)
	Start() error
	Wait() error
	Kill() error
}

type command struct{ *exec.Cmd }

func (c command) StdinPipe() (io.WriteCloser, error) { return c.Cmd.StdinPipe() }
func (c command) StdoutPipe() (io.ReadCloser, error) { return c.Cmd.StdoutPipe() }
func (c command) StderrPipe() (io.ReadCloser, error) { return c.Cmd.StderrPipe() }
func (c command) Start() error                       { return c.Cmd.Start() }
func (c command) Wait() error                        { return c.Cmd.Wait() }

func (c command) Kill() error {
	if c.Cmd.Process == nil {
		return nil
	}

	return c.Cmd.Process.Kill()
}

var processFactory = func(ctx context.Context, dir string) process {
	c := exec.CommandContext(ctx, "codex", "app-server", "--stdio")
	c.Dir = dir
	return command{c}
}

type rpcClient struct {
	in            io.WriteCloser
	out           io.ReadCloser
	enc           *json.Encoder
	mu            sync.Mutex
	next          int64
	pending       map[int64]chan response
	notifications chan notification
	routes        map[string]chan notification
	dropped       atomic.Int64
	done          chan struct{}
	writeMu       sync.Mutex
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func newRPC(in io.WriteCloser, out io.ReadCloser) *rpcClient {
	c := &rpcClient{
		in: in, out: out, enc: json.NewEncoder(in),
		pending:       map[int64]chan response{},
		notifications: make(chan notification, 256),
		routes:        map[string]chan notification{},
		done:          make(chan struct{}),
	}

	go c.read()

	return c
}

func (c *rpcClient) read() {
	s := bufio.NewScanner(c.out)
	s.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)

	for s.Scan() {
		var m rpcMessage

		if json.Unmarshal(s.Bytes(), &m) != nil {
			continue
		}

		switch {
		case len(m.ID) > 0 && m.Method != "":
			c.rejectServerRequest(m)
		case len(m.ID) > 0:
			var id int64
			if json.Unmarshal(m.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()

			if ch != nil {
				ch <- response{m.Result, m.Error}
			}
		case m.Method != "":
			c.route(notification{m.Method, m.Params})
		}
	}

	// Closing the request side as well unblocks a caller that is writing while
	// the App Server exits, and releases the process pipe promptly.
	_ = c.in.Close()
	close(c.done)
}

// rejectServerRequest ensures App Server requests never disappear into the
// response path. Ghost does not yet have an operator approval UI, so approval
// requests are explicitly declined rather than left pending forever.
func (c *rpcClient) rejectServerRequest(m rpcMessage) {
	var result any
	switch m.Method {
	case "item/fileChange/requestApproval", "item/commandExecution/requestApproval":
		result = map[string]any{"decision": "decline"}
	case "applyPatchApproval", "execCommandApproval":
		result = map[string]any{"decision": map[string]any{"denied": map[string]any{"rejection": "Ghost cannot present interactive approval requests"}}}
	default:
		c.writeReply(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(m.ID),
			"error":   map[string]any{"code": -32601, "message": "Ghost does not support this App Server request"},
		})
		return
	}
	c.writeReply(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "result": result})
}

func (c *rpcClient) writeReply(reply any) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.enc.Encode(reply)
}

// subscribe registers a per-thread notification channel. Every session owns its
// own channel, so one agent can never consume another agent's notifications.
func (c *rpcClient) subscribe(threadID string) <-chan notification {
	ch := make(chan notification, 256)

	c.mu.Lock()
	c.routes[threadID] = ch
	c.mu.Unlock()

	return ch
}

func (c *rpcClient) unsubscribe(threadID string) {
	c.mu.Lock()
	delete(c.routes, threadID)
	c.mu.Unlock()
}

// route delivers a notification to the session owning its thread. Payloads
// carrying no thread id describe the server itself and are broadcast.
func (c *rpcClient) route(n notification) {
	id := notificationThreadID(n.Params)

	c.mu.Lock()

	target, ok := c.routes[id]
	var targets []chan notification

	if !ok {
		targets = make([]chan notification, 0, len(c.routes))
		for _, ch := range c.routes {
			targets = append(targets, ch)
		}
	}

	c.mu.Unlock()

	if ok {
		c.deliver(target, n)
		return
	}

	if len(targets) == 0 {
		c.deliver(c.notifications, n)
		return
	}

	for _, ch := range targets {
		c.deliver(ch, n)
	}
}

func (c *rpcClient) deliver(ch chan notification, n notification) {
	select {
	case ch <- n:
	default:
		c.dropped.Add(1)
	}
}

// Dropped reports notifications discarded because a consumer fell behind.
func (c *rpcClient) Dropped() int64 { return c.dropped.Load() }

func notificationThreadID(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}

	var p struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}

	if json.Unmarshal(params, &p) != nil {
		return ""
	}

	if p.ThreadID != "" {
		return p.ThreadID
	}

	return p.Thread.ID
}

func (c *rpcClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()

	c.next++
	id := c.next
	ch := make(chan response, 1)
	c.pending[id] = ch

	c.mu.Unlock()

	c.writeMu.Lock()
	err := c.enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	c.writeMu.Unlock()

	if err != nil {
		c.discard(id)
		return nil, err
	}

	select {
	case r := <-ch:
		if r.Error != nil {
			return nil, fmt.Errorf("codex %s: %s", method, r.Error.Message)
		}

		return r.Result, nil
	case <-c.done:
		c.discard(id)
		return nil, ErrBackendGone
	case <-ctx.Done():
		c.discard(id)
		return nil, ctx.Err()
	}
}

func (c *rpcClient) discard(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *rpcClient) notify(method string, params any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.enc.Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
