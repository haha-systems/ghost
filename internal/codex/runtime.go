package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/haha-systems/ghost/internal/runtime"
)

type Runtime struct {
	mu       sync.Mutex
	cmd      process
	client   *rpcClient
	sessions map[string]*Session
	ctx      context.Context
	cancel   context.CancelFunc
	probed   bool
	probeErr error
}

var codexLookPath = exec.LookPath

func NewRuntime() *Runtime      { return &Runtime{sessions: map[string]*Session{}} }
func (r *Runtime) Name() string { return "codex" }

func (r *Runtime) Capabilities() runtime.Capabilities {
	return runtime.Capabilities{Steering: true, Interrupt: true, ToolEvents: true, ReasoningEvents: true}
}

func (r *Runtime) Start(ctx context.Context, cfg runtime.SessionConfig) (runtime.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.probed {
		r.probed = true
		if _, err := codexLookPath("codex"); err != nil {
			r.probeErr = fmt.Errorf("codex binary not found; install it with `npm install -g @openai/codex`: %w", err)
		}
	}

	if r.probeErr != nil {
		return nil, r.probeErr
	}

	if r.client == nil {
		// The App Server lifetime must outlive the startup request context.
		r.ctx, r.cancel = context.WithCancel(context.Background())
		r.cmd = processFactory(r.ctx, cfg.WorkingDir)
		in, e := r.cmd.StdinPipe()

		if e != nil {
			return nil, e
		}

		out, e := r.cmd.StdoutPipe()
		if e != nil {
			return nil, e
		}

		stderr, e := r.cmd.StderrPipe()
		if e != nil {
			return nil, e
		}

		if e = r.cmd.Start(); e != nil {
			return nil, fmt.Errorf("start codex app-server: %w", e)
		}

		go func() { _, _ = io.Copy(io.Discard, stderr); _ = r.cmd.Wait() }()
		r.client = newRPC(in, out)

		if _, e = r.client.call(
			ctx, "initialize",
			map[string]any{"clientInfo": map[string]any{"name": "ghost", "title": "Ghost", "version": "dev"}},
		); e != nil {
			_ = r.cmd.Kill()
			r.client = nil
			return nil, e
		}

		_ = r.client.notify("initialized", map[string]any{})
	}

	// Session event listeners belong to the runtime lifetime, not the bounded
	// startup request context.
	s, e := newSession(r.ctx, r.client, cfg)

	if e == nil {
		r.sessions[cfg.AgentID] = s
	}

	return s, e
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, s := range r.sessions {
		_ = s.Close()
	}

	if r.cancel != nil {
		r.cancel()
	}

	if r.cmd != nil {
		return r.cmd.Kill()
	}

	return nil
}

type threadInfo struct{ ID, Model string }

func startThread(ctx context.Context, c *rpcClient, cfg runtime.SessionConfig) (threadInfo, error) {
	p := map[string]any{"cwd": cfg.WorkingDir}

	if cfg.Model != "" {
		p["model"] = cfg.Model
	}

	if cfg.Instructions != "" {
		p["developerInstructions"] = cfg.Instructions
	}

	switch cfg.Sandbox {
	case runtime.SandboxDisabled:
		p["sandbox"] = "danger-full-access"
	case runtime.SandboxEnabled:
		p["sandbox"] = "workspace-write"
	}

	if cfg.Approvals == runtime.ApprovalNever {
		p["approvalPolicy"] = "never"
	}

	b, e := c.call(ctx, "thread/start", p)

	if e != nil {
		return threadInfo{}, e
	}

	var v struct {
		Model  string `json:"model"`
		Thread struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"thread"`
	}

	if json.Unmarshal(b, &v) != nil {
		return threadInfo{}, fmt.Errorf("invalid thread/start response")
	}

	if v.Thread.ID == "" {
		return threadInfo{}, fmt.Errorf("thread/start returned no id")
	}

	if v.Model == "" {
		v.Model = v.Thread.Model
	}

	return threadInfo{ID: v.Thread.ID, Model: v.Model}, nil
}
