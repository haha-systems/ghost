// Package serve is the headless JSON-RPC surface over run.Controller.
package serve

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/run"
	"github.com/haha-systems/ghost/internal/runtime"
)

const (
	codeParse    = -32700
	codeInvalid  = -32600
	codeMethod   = -32601
	codeParams   = -32602
	codeInternal = -32603
	codeNotFound = -32002
	codeConflict = -32003
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type wireEvent struct {
	SchemaVersion int               `json:"schema_version"`
	RunID         string            `json:"run_id"`
	Seq           uint64            `json:"seq"`
	Time          time.Time         `json:"time"`
	Source        string            `json:"source"`
	Kind          string            `json:"kind"`
	Message       string            `json:"message"`
	WorkID        string            `json:"work_id,omitempty"`
	PhaseRunID    string            `json:"phase_run_id,omitempty"`
	SessionID     string            `json:"session_id,omitempty"`
	TurnID        string            `json:"turn_id,omitempty"`
	Meta          map[string]string `json:"meta,omitempty"`
}

type storedCall struct {
	hash   string
	result any
	done   bool
}

// Server serves one controller. One run is active at a time.
type Server struct {
	cfg        config.Config
	logger     *slog.Logger
	rt         runtime.Runtime
	workDir    string
	controller *run.Controller
	events     <-chan run.Event
	stopSub    func()

	mu      sync.Mutex
	runID   string
	actor   string
	seq     uint64
	journal []wireEvent
	keys    map[string]storedCall
	subs    map[string]chan wireEvent
	lock    *os.File
	closed  bool
	once    sync.Once
}

// Option configures a server.
type Option func(*Server)

// WithRuntime injects the phase runtime. The default is the configured one.
func WithRuntime(rt runtime.Runtime) Option { return func(s *Server) { s.rt = rt } }

// WithWorkDir sets the root for the controller's durable core event log.
func WithWorkDir(workdir string) Option { return func(s *Server) { s.workDir = workdir } }

// New starts a controller and its event journal.
func New(cfg config.Config, logger *slog.Logger, options ...Option) (*Server, error) {
	s := &Server{cfg: cfg, logger: logger, keys: map[string]storedCall{}, subs: map[string]chan wireEvent{}}
	for _, option := range options {
		option(s)
	}
	s.controller = run.New(cfg, logger, run.Options{Runtime: s.rt, WorkDir: s.workDir, MemoryFactory: run.MemoryFromConfig(cfg.Memory)})
	events, stop := s.controller.Subscribe()
	s.events = events
	s.stopSub = stop
	return s, nil
}

func (s *Server) consume() {
	for event := range s.events {
		s.mu.Lock()
		runID := s.runID
		s.mu.Unlock()
		if runID == "" {
			continue
		}
		for _, wire := range wiresFrom(runID, event) {
			s.publish(wire)
		}
	}
}

func (s *Server) publish(event wireEvent) {
	s.mu.Lock()
	s.seq++
	event.Seq = s.seq
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	s.journal = append(s.journal, event)
	subs := make([]chan wireEvent, 0, len(s.subs))
	ids := make([]string, 0, len(s.subs))
	for id, ch := range s.subs {
		subs = append(subs, ch)
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for i, ch := range subs {
		select {
		case ch <- event:
		default:
			s.dropSub(ids[i])
		}
	}
}

func (s *Server) dropSub(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.subs[id]; ok {
		delete(s.subs, id)
		close(ch)
	}
}

// Close releases the controller and any workspace lock.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	stop := s.stopSub
	lock := s.lock
	s.lock = nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
	err := s.controller.Close()
	if lock != nil {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		err = errors.Join(err, lock.Close())
	}
	return err
}

// Serve reads newline-delimited JSON-RPC from in and writes responses to out.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.once.Do(func() { go s.consume() })
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		for _, resp := range s.dispatchLine(line) {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func (s *Server) dispatchLine(line []byte) []response {
	if len(line) > 0 && line[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(line, &batch); err != nil {
			return []response{{JSONRPC: "2.0", Error: &rpcError{Code: codeParse, Message: "invalid json"}}}
		}
		out := make([]response, 0, len(batch))
		for _, item := range batch {
			if resp, ok := s.dispatchOne(item); ok {
				out = append(out, resp)
			}
		}
		return out
	}
	if resp, ok := s.dispatchOne(line); ok {
		return []response{resp}
	}
	return nil
}

func (s *Server) dispatchOne(raw json.RawMessage) (response, bool) {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return response{JSONRPC: "2.0", Error: &rpcError{Code: codeParse, Message: "invalid json"}}, true
	}
	note := len(req.ID) == 0 || string(req.ID) == "null"
	if req.JSONRPC != "2.0" || req.Method == "" {
		if note {
			return response{}, false
		}
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: codeInvalid, Message: "invalid request"}}, true
	}
	if note {
		if s.logger != nil {
			s.logger.Warn("rejected notification", "method", req.Method)
		}
		return response{}, false
	}
	result, rpcErr := s.call(req.Method, req.Params)
	resp := response{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	return resp, true
}

func (s *Server) call(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "run.start":
		return s.start(params)
	case "run.steer":
		return s.steer(params)
	case "run.cancel":
		return s.cancel(params)
	case "run.status":
		return s.status(params)
	case "qac.override":
		return s.override(params)
	case "approval.respond":
		return s.respond(params)
	case "events.subscribe":
		return s.subscribe(params)
	default:
		return nil, &rpcError{Code: codeMethod, Message: "method not found"}
	}
}

type startParams struct {
	Workspace  string `json:"workspace"`
	Task       string `json:"task"`
	Actor      string `json:"actor"`
	RequestKey string `json:"request_key"`
}

func (s *Server) start(raw json.RawMessage) (any, *rpcError) {
	var params startParams
	if err := json.Unmarshal(raw, &params); err != nil || strings.TrimSpace(params.Task) == "" || params.RequestKey == "" || params.Actor == "" {
		return nil, invalid("workspace, task, actor, and request_key are required")
	}
	if cached, err := s.begin(params.RequestKey, params); err != nil || cached != nil {
		return cached, err
	}
	fail := func(err *rpcError) (any, *rpcError) {
		s.abort(params.RequestKey)
		return nil, err
	}
	workspace, err := matchWorkspace(s.cfg, params.Workspace)
	if err != nil {
		return fail(invalid(err.Error()))
	}
	lock, err := lockWorkspace(workspace)
	if err != nil {
		return fail(&rpcError{Code: codeConflict, Message: "workspace busy", Data: map[string]any{"reason": "workspace_busy", "retryable": true}})
	}
	_ = s.controller.SetEventLogWorkDir(workspace)
	if err := s.controller.Start(params.Task); err != nil {
		_ = unlock(lock)
		return fail(&rpcError{Code: codeConflict, Message: err.Error(), Data: map[string]any{"reason": "conflict", "retryable": false}})
	}
	status := s.controller.Status()
	workID, state := taskStatus(s.controller, status)
	s.mu.Lock()
	s.runID = "run-" + params.RequestKey
	s.actor = params.Actor
	s.lock = lock
	runID := s.runID
	seq := s.seq
	s.mu.Unlock()
	result := map[string]any{"run_id": runID, "work_id": workID, "state": state, "seq": seq}
	s.finish(params.RequestKey, result)
	return result, nil
}

type idParams struct {
	RunID              string `json:"run_id"`
	Text               string `json:"text"`
	RequestKey         string `json:"request_key"`
	Reason             string `json:"reason"`
	ResourceID         string `json:"resource_id"`
	Clear              bool   `json:"clear"`
	ExpectedPhaseRunID string `json:"expected_phase_run_id"`
	AfterSeq           uint64 `json:"after_seq"`
}

func (s *Server) steer(raw json.RawMessage) (any, *rpcError) {
	var params idParams
	if err := json.Unmarshal(raw, &params); err != nil || params.RequestKey == "" || params.Text == "" {
		return nil, invalid("run_id, text, and request_key are required")
	}
	if cached, err := s.begin(params.RequestKey, params); err != nil || cached != nil {
		return cached, err
	}
	if err := s.knownRun(params.RunID); err != nil {
		s.abort(params.RequestKey)
		return nil, err
	}
	status := s.controller.Status()
	if params.ExpectedPhaseRunID != "" && params.ExpectedPhaseRunID != status.PhaseRunID {
		s.abort(params.RequestKey)
		return nil, &rpcError{Code: codeConflict, Message: "stale phase", Data: map[string]any{"reason": "stale_phase", "run_id": params.RunID, "retryable": false}}
	}
	if err := s.controller.Steer(params.Text); err != nil {
		s.abort(params.RequestKey)
		return nil, internal(err)
	}
	delivery := "queued"
	if s.controller.Status().Active {
		delivery = "live"
	}
	result := map[string]any{"instruction_id": params.RequestKey, "delivery": delivery}
	s.finish(params.RequestKey, result)
	return result, nil
}

func (s *Server) cancel(raw json.RawMessage) (any, *rpcError) {
	var params idParams
	if err := json.Unmarshal(raw, &params); err != nil || params.RequestKey == "" {
		return nil, invalid("run_id and request_key are required")
	}
	if cached, err := s.begin(params.RequestKey, params); err != nil || cached != nil {
		return cached, err
	}
	if err := s.knownRun(params.RunID); err != nil {
		s.abort(params.RequestKey)
		return nil, err
	}
	if err := s.controller.Cancel(); err != nil {
		s.abort(params.RequestKey)
		return nil, internal(err)
	}
	status := s.controller.Status()
	_, state := taskStatus(s.controller, status)
	result := map[string]any{"state": state, "reason": "operator_canceled"}
	s.finish(params.RequestKey, result)
	s.publish(wireEvent{SchemaVersion: 1, RunID: params.RunID, Source: "GHOST", Kind: "run.finished", Message: "operator canceled", Meta: map[string]string{"state": state}})
	return result, nil
}

func (s *Server) status(raw json.RawMessage) (any, *rpcError) {
	var params idParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, invalid("run_id is required")
	}
	if err := s.knownRun(params.RunID); err != nil {
		return nil, err
	}
	status := s.controller.Status()
	workID, state := taskStatus(s.controller, status)
	s.mu.Lock()
	seq := s.seq
	actor := s.actor
	s.mu.Unlock()
	return map[string]any{
		"run_id": params.RunID, "work_id": workID, "state": state, "phase": string(status.State.Task.Phase),
		"reason": status.State.Task.TerminalReason, "resource": status.Resource, "phase_run_id": status.PhaseRunID,
		"actor": actor, "seq": seq, "active": status.Active,
	}, nil
}

func (s *Server) override(raw json.RawMessage) (any, *rpcError) {
	var params idParams
	if err := json.Unmarshal(raw, &params); err != nil || params.RequestKey == "" {
		return nil, invalid("run_id and request_key are required")
	}
	if cached, err := s.begin(params.RequestKey, params); err != nil || cached != nil {
		return cached, err
	}
	if err := s.knownRun(params.RunID); err != nil {
		s.abort(params.RequestKey)
		return nil, err
	}
	var callErr error
	if params.Clear {
		callErr = s.controller.ClearOverride()
	} else if params.ResourceID == "" {
		s.abort(params.RequestKey)
		return nil, invalid("resource_id or clear is required")
	} else {
		callErr = s.controller.Override(params.ResourceID, params.Reason)
	}
	if callErr != nil {
		s.abort(params.RequestKey)
		return nil, invalid(callErr.Error())
	}
	result := map[string]any{"resource_id": params.ResourceID, "clear": params.Clear, "boundary": "next_allocation"}
	s.finish(params.RequestKey, result)
	return result, nil
}

type approvalParams struct {
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"`
	Responder  string `json:"responder"`
	Note       string `json:"note"`
	RequestKey string `json:"request_key"`
}

func (s *Server) respond(raw json.RawMessage) (any, *rpcError) {
	var params approvalParams
	if err := json.Unmarshal(raw, &params); err != nil || params.ApprovalID == "" || params.RequestKey == "" || params.Responder == "" {
		return nil, invalid("approval_id, decision, responder, and request_key are required")
	}
	if cached, err := s.begin(params.RequestKey, params); err != nil || cached != nil {
		return cached, err
	}
	approval, err := s.controller.RespondApproval(params.ApprovalID, params.Decision, params.Responder, params.Note)
	if err != nil {
		s.abort(params.RequestKey)
		code := codeNotFound
		if strings.Contains(err.Error(), "already resolved") {
			code = codeConflict
		}
		if strings.Contains(err.Error(), "decision must") {
			code = codeParams
		}
		return nil, &rpcError{Code: code, Message: err.Error(), Data: map[string]any{"reason": "approval", "retryable": false}}
	}
	result := map[string]any{"approval_id": approval.ID, "decision": approval.Decision, "responder": approval.Responder, "note": approval.Note}
	s.finish(params.RequestKey, result)
	return result, nil
}

func (s *Server) subscribe(raw json.RawMessage) (any, *rpcError) {
	var params idParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, invalid("run_id is required")
	}
	if err := s.knownRun(params.RunID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if params.AfterSeq > s.seq {
		return nil, &rpcError{Code: codeParams, Message: "cursor beyond the end", Data: map[string]any{"reason": "cursor", "retryable": false}}
	}
	id := fmt.Sprintf("sub-%d", len(s.subs)+1)
	ch := make(chan wireEvent, 32)
	var replay []wireEvent
	for _, event := range s.journal {
		if event.Seq > params.AfterSeq {
			replay = append(replay, event)
		}
	}
	go func() {
		for _, event := range replay {
			ch <- event
		}
	}()
	s.subs[id] = ch
	return map[string]any{"subscription_id": id, "seq": s.seq}, nil
}

// Notifications returns the live channel for a subscription created by events.subscribe.
func (s *Server) Notifications(id string) (<-chan wireEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.subs[id]
	return ch, ok
}

func (s *Server) knownRun(id string) *rpcError {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runID == "" || id != s.runID {
		return &rpcError{Code: codeNotFound, Message: "run not found", Data: map[string]any{"reason": "run_not_found", "run_id": id, "retryable": false}}
	}
	return nil
}

func (s *Server) begin(key string, params any) (any, *rpcError) {
	hash := hashParams(params)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.keys[key]; ok {
		if existing.hash != hash {
			return nil, &rpcError{Code: codeConflict, Message: "request key reused", Data: map[string]any{"reason": "request_key", "retryable": false}}
		}
		if !existing.done {
			return nil, &rpcError{Code: codeConflict, Message: "request in progress", Data: map[string]any{"reason": "request_key", "retryable": true}}
		}
		return existing.result, nil
	}
	s.keys[key] = storedCall{hash: hash}
	return nil, nil
}

func (s *Server) finish(key string, result any) {
	s.mu.Lock()
	existing := s.keys[key]
	existing.result = result
	existing.done = true
	s.keys[key] = existing
	s.mu.Unlock()
}

func (s *Server) abort(key string) {
	s.mu.Lock()
	delete(s.keys, key)
	s.mu.Unlock()
}

func hashParams(params any) string {
	data, _ := json.Marshal(params)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func wiresFrom(runID string, event run.Event) []wireEvent {
	var out []wireEvent
	if event.Entry.Kind != "" || event.Entry.Message != "" {
		out = append(out, wireEvent{
			SchemaVersion: 1, RunID: runID, Time: event.Entry.Time, Source: event.Entry.Source,
			Kind: string(event.Entry.Kind), Message: event.Entry.Message, SessionID: event.Entry.SessionID,
			TurnID: event.Entry.TurnID, Meta: event.Entry.Metadata,
		})
	}
	if event.Phase != nil {
		out = append(out, wireEvent{
			SchemaVersion: 1, RunID: runID, Time: event.Phase.Time, Source: "CES", Kind: event.Phase.Name,
			Message: event.Phase.Name, WorkID: event.Phase.WorkID, PhaseRunID: event.Phase.RunID,
			SessionID: event.Phase.SessionID, TurnID: event.Phase.TurnID, Meta: event.Phase.Fields,
		})
	}
	return out
}

func invalid(message string) *rpcError {
	return &rpcError{Code: codeParams, Message: message, Data: map[string]any{"reason": "invalid_params", "retryable": false}}
}

func taskStatus(controller *run.Controller, status run.Status) (string, string) {
	if status.State.Task.ID != "" {
		return string(status.State.Task.ID), string(status.State.Task.Status)
	}
	tasks := controller.WorkspaceSnapshot().Tasks
	if len(tasks) == 0 {
		return "", ""
	}
	task := tasks[len(tasks)-1]
	return string(task.ID), string(task.Status)
}

func internal(err error) *rpcError {
	return &rpcError{Code: codeInternal, Message: err.Error(), Data: map[string]any{"reason": "internal", "retryable": false}}
}

func matchWorkspace(cfg config.Config, workspace string) (string, error) {
	resolved, err := resolvePath(workspace)
	if err != nil {
		return "", err
	}
	if cfg.Agents == nil {
		return "", errors.New("no workspaces are configured")
	}
	for _, agent := range *cfg.Agents {
		dir := agent.WorkingDir
		if dir == "" {
			dir = "."
		}
		allowed, err := resolvePath(dir)
		if err != nil {
			continue
		}
		if resolved == allowed {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("unknown workspace %q", workspace)
}

func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("workspace path is a symlink")
	}
	return resolved, nil
}

func lockWorkspace(dir string) (*os.File, error) {
	ghost := filepath.Join(dir, ".ghost")
	if err := os.MkdirAll(ghost, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(ghost, "run.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func unlock(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return errors.Join(err, file.Close())
}

// ListenSocket opens a user-only Unix socket. A live socket is left in place.
func ListenSocket(path string) (*net.UnixListener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("socket path is a symlink")
		}
		if conn, err := net.Dial("unix", path); err == nil {
			_ = conn.Close()
			return nil, errors.New("socket is already live")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener.(*net.UnixListener), nil
}

// AcceptSameUser accepts one connection from the server's own UID.
func AcceptSameUser(listener *net.UnixListener) (net.Conn, error) {
	conn, err := listener.AcceptUnix()
	if err != nil {
		return nil, err
	}
	if err := sameUser(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// RunOnce runs one task and writes NDJSON event envelopes to out.
func RunOnce(cfg config.Config, logger *slog.Logger, task, workspace string, out io.Writer, options ...Option) (int, error) {
	if _, err := matchWorkspace(cfg, workspace); err != nil {
		return 2, err
	}
	configuredOptions := append([]Option(nil), options...)
	configuredOptions = append(configuredOptions, WithWorkDir(workspace))
	server, err := New(cfg, logger, configuredOptions...)
	if err != nil {
		return 1, err
	}
	defer func() { _ = server.Close() }()
	params, _ := json.Marshal(startParams{Workspace: workspace, Task: task, Actor: "cli", RequestKey: "run-once"})
	result, rpcErr := server.start(params)
	if rpcErr != nil {
		if rpcErr.Code == codeConflict && strings.Contains(rpcErr.Message, "busy") {
			return 4, errors.New(rpcErr.Message)
		}
		if rpcErr.Code == codeParams {
			return 2, errors.New(rpcErr.Message)
		}
		return 1, errors.New(rpcErr.Message)
	}
	enc := json.NewEncoder(out)
	runID, _ := result.(map[string]any)["run_id"].(string)
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-server.events:
			if !ok {
				return 1, errors.New("event stream closed")
			}
			for _, wire := range wiresFrom(runID, event) {
				wire.Seq = 0
				if err := enc.Encode(wire); err != nil {
					_ = server.controller.Cancel()
					return 1, err
				}
			}
			if event.Boundary && event.Status != nil && event.Status.State.Task.ID != "" && !event.Status.Active {
				state := event.Status.State.Task.Status
				finished := wireEvent{SchemaVersion: 1, RunID: runID, Source: "GHOST", Kind: "run.finished", Message: string(state), Meta: map[string]string{"state": string(state), "reason": event.Status.State.Task.TerminalReason}}
				if err := enc.Encode(finished); err != nil {
					return 1, err
				}
				if state == "complete" {
					return 0, nil
				}
				return 3, nil
			}
		case <-deadline.C:
			_ = server.controller.Cancel()
			return 1, errors.New("run timed out")
		}
	}
}
