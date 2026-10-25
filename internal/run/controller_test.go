package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	ghosttrace "github.com/haha-systems/ghost/internal/trace"
	"github.com/haha-systems/qac"
)

var chainArtifacts = []string{
	`{"classification":"defect","unknowns":[{"local_ref":"u1","question":"which layer drops the sequence"}],"next_investigation":"capture the sequence at the boundary"}`,
	`{"hypotheses":[{"local_ref":"h1","mechanism":"the sequence is dropped before rendering","falsifier":"a capture showing it arriving"}],"leading_hypothesis_ref":"h1"}`,
	`{"frame":{"local_ref":"f1","name":"carry the sequence","summary":"make the sequence visible at the boundary","completion_conditions":["the sequence renders"]}}`,
	`{"actions":[{"local_ref":"a1","description":"carry the sequence through the boundary"}],"outcomes":[{"local_ref":"x1","description":"the sequence renders"}]}`,
	`{"observations":[{"local_ref":"v1","content":"the sequence renders"}],"residual_uncertainty":"","completion_recommended":true}`,
}

// scriptRuntime uses real fake sessions and adds deterministic answers to Send.
// The fake guard and event channel still implement session state and teardown.
type scriptRuntime struct {
	*fake.Runtime
	mu       sync.Mutex
	scripts  [][]string
	configs  []runtime.SessionConfig
	sessions []*scriptSession
	started  chan *scriptSession
	gate     <-chan struct{}
}
type scriptSession struct {
	*fake.Session
	script []string
	sends  int
	inputs []runtime.Input
}

func (r *scriptRuntime) Start(ctx context.Context, cfg runtime.SessionConfig) (runtime.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := len(r.configs)
	if index >= len(r.scripts) {
		return nil, errors.New("no fake script")
	}
	s, err := r.Runtime.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	session := &scriptSession{Session: s.(*fake.Session), script: r.scripts[index]}
	r.configs = append(r.configs, cfg)
	r.sessions = append(r.sessions, session)
	if r.started != nil {
		r.started <- session
	}
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return session, nil
}
func (s *scriptSession) Send(ctx context.Context, input runtime.Input) error {
	if err := s.Session.Send(ctx, input); err != nil {
		return err
	}
	s.inputs = append(s.inputs, input)
	if len(s.script) == 0 {
		return nil
	}
	index := s.sends
	if index >= len(s.script) {
		index = len(s.script) - 1
	}
	s.sends++
	s.Guard.Complete("fake-turn", false)
	s.Emit(runtime.Event{AgentID: s.Agent, SessionID: s.ID(), TurnID: "fake-turn", Kind: runtime.KindMessage, Summary: s.script[index], Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}})
	s.Emit(runtime.Event{AgentID: s.Agent, SessionID: s.ID(), TurnID: "fake-turn", Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}})
	return nil
}
func newScript(t *testing.T, scripts ...[]string) (*Controller, *scriptRuntime, <-chan Event) {
	t.Helper()
	rt := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scripts, started: make(chan *scriptSession, 20)}
	c := New(appQACConfig(), nil, Options{Runtime: rt})
	_ = c.SetSessions(map[string]runtime.Session{"wraith": fake.NewSession("wraith"), "shade": fake.NewSession("shade")})
	events, unsubscribe := c.Subscribe()
	t.Cleanup(func() {
		unsubscribe()
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c, rt, events
}
func scriptsFor(values []string) [][]string {
	out := [][]string{}
	for _, v := range values {
		out = append(out, []string{v})
	}
	return out
}
func nextEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case e, ok := <-events:
		if !ok {
			t.Fatal("subscription closed")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("run event timeout")
		return Event{}
	}
}
func terminalEvents(t *testing.T, events <-chan Event) (Status, []Event) {
	t.Helper()
	all := []Event{}
	for {
		e := nextEvent(t, events)
		all = append(all, e)
		if e.Boundary && e.Status != nil && e.Status.State.Task.ID != "" && !e.Status.Active {
			return *e.Status, all
		}
	}
}
func TestControllerCloseWithoutVerificationStaysUnderdetermined(t *testing.T) {
	artifacts := append([]string{}, chainArtifacts[:4]...)
	artifacts = append(artifacts, `{"residual_uncertainty":"verification missing","completion_recommended":true}`)
	c, _, events := newScript(t, scriptsFor(artifacts)...)
	if err := c.Start("verify the boundary"); err != nil {
		t.Fatal(err)
	}
	status, _ := terminalEvents(t, events)
	if status.State.Task.Status != epistemic.WorkIncomplete || status.State.Task.TerminalReason != "underdetermined" {
		t.Fatalf("task=%#v", status.State.Task)
	}
}

func TestControllerFullPhaseChainAndSubscriberOrder(t *testing.T) {
	c, rt, events := newScript(t, scriptsFor(chainArtifacts)...)
	second, stop := c.Subscribe()
	defer stop()
	if err := c.Start("make the sequence visible"); err != nil {
		t.Fatal(err)
	}
	status, all := terminalEvents(t, events)
	if status.State.Task.Status != epistemic.WorkComplete || status.Work.State != cognition.WorkComplete {
		t.Fatalf("terminal=%#v", status)
	}
	_, copyEvents := terminalEvents(t, second)
	if len(all) != len(copyEvents) {
		t.Fatalf("subscriber lengths %d/%d", len(all), len(copyEvents))
	}
	finished := map[string]bool{}
	for i, e := range all {
		if e.Phase != nil && e.Phase.Name == cognition.PhaseFinished {
			finished[e.Phase.RunID] = true
		}
		if e.Entry.Kind == event.KindCES && strings.Contains(e.Entry.Message, "artifact accepted") && !finished[e.Entry.Metadata["phase_run_id"]] {
			t.Fatal("phase result preceded observer completion")
		}
		if fmt.Sprint(e.Entry) != fmt.Sprint(copyEvents[i].Entry) {
			t.Fatal("subscriber order differs")
		}
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.configs) != 5 {
		t.Fatalf("phase sessions=%d", len(rt.configs))
	}
	for i, cfg := range rt.configs {
		phase := []string{"TRIAGE", "ABDUCE", "FRAME", "EXECUTE", "CLOSE"}[i]
		if !strings.Contains(cfg.Instructions, "CES PHASE: "+phase) {
			t.Fatalf("missing %s contract", phase)
		}
	}
}
func TestControllerRepairUsesSameSession(t *testing.T) {
	scripts := scriptsFor(chainArtifacts)
	scripts[0] = []string{`{"classification":"defect","unexpected":true}`, chainArtifacts[0]}
	c, rt, events := newScript(t, scripts...)
	_ = c.Start("repair artifact")
	status, all := terminalEvents(t, events)
	if status.State.Task.Status != epistemic.WorkComplete {
		t.Fatalf("task=%#v", status.State.Task)
	}
	repaired := false
	for _, e := range all {
		repaired = repaired || e.Phase != nil && e.Phase.Name == cognition.PhaseArtifactRepairSucceeded
	}
	if !repaired {
		t.Fatal("no repair success")
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.sessions) != 5 || rt.sessions[0].sends != 2 {
		t.Fatal("repair did not reuse the producing session")
	}
}
func TestControllerRepeatLimit(t *testing.T) {
	contradiction := `{"observations":[{"local_ref":"o1","content":"still missing"}],"actions":[{"local_ref":"a1","description":"carry sequence"}],"relations":[{"local_ref":"r1","kind":"contradicts","source":"o1","target":"a1"}]}`
	values := append([]string{}, chainArtifacts[:3]...)
	for range 5 {
		values = append(values, contradiction)
	}
	c, _, events := newScript(t, scriptsFor(values)...)
	_ = c.Start("bounded repeats")
	status, _ := terminalEvents(t, events)
	if status.State.Task.Status != epistemic.WorkIncomplete || status.State.Task.Phase == epistemic.PhaseClose || status.State.Task.TerminalReason != "reopen_budget_exhausted" {
		t.Fatalf("task=%#v", status.State.Task)
	}
}
func TestControllerSteerDuringActiveWorkAndCancel(t *testing.T) {
	c, rt, events := newScript(t, []string{})
	_ = c.Start("active work")
	session := <-rt.started
	// Wait for the turn before recording steering.
	for {
		e := nextEvent(t, events)
		if e.Phase != nil && e.Phase.Name == cognition.PhaseTurnRequested {
			break
		}
	}
	if err := c.Steer("check the footer"); err != nil {
		t.Fatal(err)
	}
	recorded := false
	for {
		e := nextEvent(t, events)
		recorded = recorded || e.Entry.Message == "steering recorded during TRIAGE"
		if e.Boundary {
			break
		}
	}
	if !recorded {
		t.Fatal("steering not recorded")
	}
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	status, _ := terminalEvents(t, events)
	if status.State.Task.Status != epistemic.WorkIncomplete || status.State.Task.TerminalReason != "operator_canceled" {
		t.Fatalf("cancel=%#v", status.State.Task)
	}
	_ = c.call(func(o *owner) {})
	// Close waits for teardown, then the fake session must be stopped.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if session.State() != runtime.StateStopped {
		t.Fatalf("session state=%s", session.State())
	}
	if len(session.inputs) != 1 || session.LastSteer != "" {
		t.Fatal("active CES steering reached a runtime session")
	}
}
func TestControllerTraceFailureStopsBeforeCommit(t *testing.T) {
	c, _, events := newScript(t, []string{})
	w, err := ghosttrace.Open(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.call(func(o *owner) { o.trace = w })
	_ = c.Start("trace failure")
	for {
		e := nextEvent(t, events)
		if e.Phase != nil && e.Phase.Name == cognition.PhaseTurnRequested {
			break
		}
	}
	_ = w.Close()
	_ = c.Record("", event.Event{Source: "test", Message: "write"}, nil)
	status, all := terminalEvents(t, events)
	if status.TraceError == nil || status.Active || len(status.State.Observations) != 0 {
		t.Fatalf("trace failure=%#v", status)
	}
	found := false
	for _, e := range all {
		found = found || strings.Contains(e.Entry.Message, "trace write failed")
	}
	if !found {
		t.Fatal("trace failure warning missing")
	}
	// Avoid a second file close in cleanup; the failed writer stays owned until now.
	_ = c.call(func(o *owner) { o.trace = nil })
}
func TestFailedTerminalTransitionMarksRunIncomplete(t *testing.T) {
	store, err := epistemic.NewStore("terminal failure")
	if err != nil {
		t.Fatal(err)
	}
	controller := cesController{store: store}
	terminal := controller.terminal(epistemic.WorkComplete, "", "")
	if terminal.status != epistemic.WorkIncomplete || !strings.Contains(terminal.detail, "set terminal work status") {
		t.Fatalf("terminal=%#v", terminal)
	}
	if store.State().Task.Status != epistemic.WorkIncomplete || store.State().Task.TerminalReason != "phase_execution_failed" {
		t.Fatalf("task=%#v", store.State().Task)
	}
}
func TestFinalTraceFailureOverridesTerminalStatus(t *testing.T) {
	c, _, events := newScript(t, []string{})
	_ = c.Start("final trace failure")
	_ = c.call(func(o *owner) {
		_, err := o.cesStore.SetTerminal(epistemic.WorkIncomplete, "underdetermined")
		if err != nil {
			t.Error(err)
		}
	})
	w, err := ghosttrace.Open(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	_ = c.call(func(o *owner) {
		o.trace = w
		o.traceCES(o.newCESRun(epistemic.PhaseTriage, "wraith"), "final", "write", "", "", nil)
		o.publishBoundary()
	})
	status, _ := terminalEvents(t, events)
	if status.State.Task.TerminalReason != "phase_execution_failed" {
		t.Fatalf("task=%#v", status.State.Task)
	}
	_ = c.call(func(o *owner) {
		replay, err := epistemic.Replay(o.cesStore.Events())
		if err != nil {
			t.Error(err)
		} else if replay.State().Task.TerminalReason != "phase_execution_failed" {
			t.Error("replay lost run failure")
		}
		o.trace = nil
	})
}
func TestQACStopUpdatesCESAsIncompleteAndPublishesSemanticEvent(t *testing.T) {
	c, _, events := newScript(t, []string{})
	_ = c.Start("restore prompt delivery")
	_ = c.call(func(o *owner) {
		o.handleCognitionResult(cognitionResultMsg{plan: cognition.Plan{WorkID: o.cognition.Work().ID, Action: qac.ActionStop}})
		o.publishBoundary()
	})
	status, all := terminalEvents(t, events)
	if status.State.Task.Status != epistemic.WorkIncomplete || status.Work.State != cognition.WorkIncomplete {
		t.Fatalf("status=%#v", status)
	}
	found := false
	for _, e := range all {
		found = found || e.Entry.Kind == event.KindCES && e.Entry.Metadata["semantic_kind"] == string(epistemic.SemanticWorkIncomplete)
	}
	if !found {
		t.Fatal("WorkIncomplete semantic event missing")
	}
}

func TestObserverTraceFailureRejectsPhaseResult(t *testing.T) {
	c, rt, events := newScript(t, scriptsFor(chainArtifacts)...)
	gate := make(chan struct{})
	rt.gate = gate
	w, err := ghosttrace.Open(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.call(func(o *owner) { o.trace = w })
	if err := c.Start("observer failure"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rt.started:
	case <-time.After(5 * time.Second):
		t.Fatal("phase startup timeout")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	close(gate)
	status, _ := terminalEvents(t, events)
	if status.TraceError == nil || status.State.Task.Status != epistemic.WorkIncomplete || len(status.State.Observations) != 0 {
		t.Fatalf("failed observer committed work: %#v", status)
	}
	_ = c.call(func(o *owner) { o.trace = nil })
}

func TestSnapshotsAndSubscribersHaveIndependentValues(t *testing.T) {
	c, _, events := newScript(t, []string{})
	second, stop := c.Subscribe()
	defer stop()
	_ = c.Start("original goal")
	for {
		e := nextEvent(t, events)
		if e.Status != nil && e.Status.State.Task.ID != "" {
			e.Status.State.Task.Goal = "changed by client"
			if e.Status.Work != nil {
				e.Status.Work.Goal = "changed work"
			}
			break
		}
	}
	status := c.Status()
	status.State.Task.Goal = "changed snapshot"
	if current := c.Status(); current.State.Task.Goal != "original goal" || current.Work.Goal != "original goal" {
		t.Fatal("client changed controller state")
	}
	for {
		e := nextEvent(t, second)
		if e.Status != nil && e.Status.State.Task.ID != "" {
			if e.Status.State.Task.Goal != "original goal" {
				t.Fatal("subscribers share state")
			}
			break
		}
	}
	_ = c.Cancel()
}

func TestConcurrentSteerStatusAndCancel(t *testing.T) {
	c, _, events := newScript(t, []string{})
	_ = c.Start("concurrent control")
	var callers sync.WaitGroup
	for i := range 12 {
		callers.Add(1)
		go func() { defer callers.Done(); _ = c.Steer(fmt.Sprintf("instruction %d", i)); _ = c.Status() }()
	}
	callers.Wait()
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	status, _ := terminalEvents(t, events)
	if status.Active || status.State.Task.Status != epistemic.WorkIncomplete {
		t.Fatal("cancel left concurrent run active")
	}
}

func TestLatePhaseResultCannotMutateNewCESWork(t *testing.T) {
	c, rt, _ := newScript(t, []string{}, []string{})
	_ = c.Start("first work")
	select {
	case <-rt.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first phase did not start")
	}
	var stale cesPhaseResultMsg
	artifact, err := cognition.ParsePhaseArtifact(epistemic.PhaseTriage, []byte(chainArtifacts[0]))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.call(func(o *owner) {
		stale = cesPhaseResultMsg{phase: epistemic.PhaseTriage, resource: o.cesResource, run: cesRun{id: o.cesRunID, workID: string(o.cesStore.State().Task.ID)}, result: cognition.PhaseResult{Artifact: artifact}}
	})
	_ = c.Cancel()
	_ = c.Start("second work")
	before := c.Status()
	_ = c.call(func(o *owner) { o.handlePhaseResult(stale) })
	after := c.Status()
	if before.State.Task.ID == epistemic.ID(stale.run.workID) || after.State.Task.ID != before.State.Task.ID || after.State.Task.Phase != epistemic.PhaseTriage || !after.Active || len(after.State.Unknowns) != 0 {
		t.Fatalf("stale result changed new work: %#v", after.State.Task)
	}
}

func TestConfigurationCopyRetainsTimingAndOwnsMaps(t *testing.T) {
	cfg := appQACConfig()
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte("[trace]\nstall_after = \"2s\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Trace = parsed.Trace
	copy := cloneConfig(cfg)
	if copy.Trace.StallAfter() != 2*time.Second || copy.Trace.StallAfter() != cfg.Trace.StallAfter() {
		t.Fatal("configuration copy lost stall timeout")
	}
	copy.QAC.Policy.Hierarchy[0] = "changed"
	backend := (*copy.Agents)["wraith"]
	backend.Model = "changed"
	(*copy.Agents)["wraith"] = backend
	if cfg.QAC.Policy.Hierarchy[0] != "wraith" || (*cfg.Agents)["wraith"].Model == "changed" {
		t.Fatal("configuration shares caller maps")
	}
}
