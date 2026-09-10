package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

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
func (c command) Kill() error                        { return c.Cmd.Process.Kill() }

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
	done          chan struct{}
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

func newRPC(in io.WriteCloser, out io.ReadCloser) *rpcClient {
	c := &rpcClient{in: in, out: out, enc: json.NewEncoder(in), pending: map[int64]chan response{}, notifications: make(chan notification, 64), done: make(chan struct{})}
	go c.read()
	return c
}
func (c *rpcClient) read() {
	s := bufio.NewScanner(c.out)
	for s.Scan() {
		var m struct {
			ID     *int64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(s.Bytes(), &m) != nil {
			continue
		}
		if m.ID != nil {
			c.mu.Lock()
			ch := c.pending[*m.ID]
			delete(c.pending, *m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- response{m.Result, m.Error}
			}
		} else if m.Method != "" {
			select {
			case c.notifications <- notification{m.Method, m.Params}:
			default:
			}
		}
	}
	close(c.done)
}
func (c *rpcClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Error != nil {
			return nil, fmt.Errorf("codex %s: %s", method, r.Error.Message)
		}
		return r.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (c *rpcClient) notify(method string, params any) error {
	return c.enc.Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
