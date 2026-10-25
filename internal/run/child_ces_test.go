package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

type childCancelRuntime struct {
	*scriptRuntime
	cancelled chan struct{}
	once      sync.Once
}

func (r *childCancelRuntime) Start(ctx context.Context, cfg runtime.SessionConfig) (runtime.Session, error) {
	defer func() {
		if ctx.Err() != nil {
			r.once.Do(func() { close(r.cancelled) })
		}
	}()
	return r.scriptRuntime.Start(ctx, cfg)
}

func newChildCESController(t *testing.T, cfg config.Config, rt runtime.Runtime) (*Controller, <-chan Event) {
	t.Helper()
	return newCESAutoController(t, cfg, rt, nil, "")
}

func configureChildCESDelegation(t *testing.T, c *Controller, policy, timeout string) (*atomic.Int32, *atomic.Bool) {
	t.Helper()
	childFrames := &atomic.Int32{}
	parentSawResult := &atomic.Bool{}
	var parentTaskID string
	var parentSends atomic.Int32
	if err := c.call(func(o *owner) {
		parent := o.sessions["wraith"].(*fake.Session)
		child := o.sessions["shade"].(*fake.Session)
		respond := func(input runtime.Input) []runtime.Event {
			if parentSends.Add(1) == 1 {
				parentTaskID = frameValue(input.Text, "task_id")
				request := map[string]any{
					"schema_version": 1, "target": "shade", "parent_task_id": parentTaskID,
					"objective": "inspect the sequence", "context_refs": []string{},
					"expected_deliverable": "one verified finding",
				}
				if policy != "" {
					request["ces_policy"] = policy
				}
				if timeout != "" {
					request["timeout"] = timeout
				}
				data, _ := json.Marshal(request)
				return channelEvent("wraith", "```ghost-delegate\n"+string(data)+"\n```")
			}
			parentSawResult.Store(strings.Contains(input.Text, "the sequence is dropped before rendering") || strings.Contains(input.Text, "failed:"))
			return channelEvent("wraith", "I reviewed the child result.")
		}
		parent.OnSend, parent.OnSteer = respond, respond
		child.OnSend = func(runtime.Input) []runtime.Event {
			childFrames.Add(1)
			return nil
		}
		child.OnSteer = child.OnSend
	}); err != nil {
		t.Fatal(err)
	}
	return childFrames, parentSawResult
}

func startDelegatedChildCES(t *testing.T, c *Controller) (core.Task, core.Task) {
	t.Helper()
	var result error
	var brokerDone <-chan error
	callErr := c.call(func(o *owner) {
		wraith, ok := o.workspace.agentID("wraith")
		if !ok {
			result = fmt.Errorf("wraith is not configured")
			return
		}
		o.startRecipients = []core.AgentID{wraith}
		policy := CESPolicyOff
		brokerDone, result = o.start("delegate sequence review", &policy)
		o.startRecipients = nil
	})
	if brokerDone != nil {
		if err := <-brokerDone; result == nil {
			result = err
		}
	}
	if callErr != nil {
		t.Fatal(callErr)
	}
	if result != nil {
		t.Fatal(result)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for len(c.WorkspaceSnapshot().Tasks) < 2 {
		select {
		case <-deadline.C:
			t.Fatalf("child was not delegated: tasks=%#v messages=%#v", c.WorkspaceSnapshot().Tasks, c.WorkspaceSnapshot().Messages)
		case <-time.After(10 * time.Millisecond):
		}
	}
	workspace := c.WorkspaceSnapshot()
	return workspace.Tasks[0], workspace.Tasks[1]
}

func waitForTaskStatus(t *testing.T, c *Controller, id core.TaskID, status core.TaskStatus) core.Task {
	t.Helper()
	var found core.Task
	waitFor(t, func() bool {
		for _, task := range c.WorkspaceSnapshot().Tasks {
			if task.ID == id {
				found = task
				return task.Status == status
			}
		}
		return false
	})
	return found
}

func scriptRuntimeState(rt *scriptRuntime) ([]runtime.SessionConfig, []*scriptSession) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]runtime.SessionConfig(nil), rt.configs...), append([]*scriptSession(nil), rt.sessions...)
}

func TestDelegatedEnforceChildRunsCESOnOwnerAndReturnsResult(t *testing.T) {
	rt := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10)}
	c, _ := newChildCESController(t, appQACConfig(), rt)
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	childFrames, parentSawResult := configureChildCESDelegation(t, c, "enforce", "")
	parent, child := startDelegatedChildCES(t, c)
	parent = waitForTaskStatus(t, c, parent.ID, core.TaskCompleted)
	child = waitForTaskStatus(t, c, child.ID, core.TaskCompleted)
	if childFrames.Load() != 0 {
		t.Fatalf("child persistent session received %d task frames", childFrames.Load())
	}
	if child.CESPolicy != core.CESEnforce {
		t.Fatalf("child CES policy = %q", child.CESPolicy)
	}
	if !parentSawResult.Load() {
		var sent, steered string
		_ = c.call(func(o *owner) {
			parent := o.sessions["wraith"].(*fake.Session)
			sent, steered = parent.LastSentText(), parent.LastSteeredText()
		})
		t.Fatalf("parent did not receive the child CES result: sent=%q steered=%q messages=%#v", sent, steered, c.WorkspaceSnapshot().Messages)
	}
	var childPhases int
	for _, event := range c.Events(EventFilter{Type: "ces.phase"}) {
		if event.TaskID == child.ID {
			childPhases++
			if !strings.Contains(string(event.Payload), `"task_id":"`+string(child.ID)+`"`) {
				t.Fatalf("child phase event lacks task ID metadata: %s", event.Payload)
			}
		}
	}
	if childPhases == 0 {
		t.Fatalf("no CES phase events were recorded for child %s", child.ID)
	}
	if qacEvents := c.Events(EventFilter{Type: "decision.qac"}); len(qacEvents) != 0 {
		t.Fatalf("child phases consulted QAC: %#v", qacEvents)
	}
	configs, sessions := scriptRuntimeState(rt)
	if len(configs) != len(chainArtifacts) {
		t.Fatalf("phase sessions = %d, want %d", len(configs), len(chainArtifacts))
	}
	for i, cfg := range configs {
		if cfg.AgentID != "shade" || len(sessions[i].inputs) == 0 || !strings.Contains(sessions[i].inputs[0].Text, "one verified finding") {
			t.Fatalf("phase session %d config/input = %#v / %#v", i, cfg, sessions[i].inputs)
		}
	}
}

func TestDelegatedChildWorkspaceOnUpgradesPolicy(t *testing.T) {
	rt := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10)}
	c, _ := newChildCESController(t, appQACConfig(), rt)
	if err := c.SetCESPolicy(CESPolicyOn); err != nil {
		t.Fatal(err)
	}
	childFrames, _ := configureChildCESDelegation(t, c, "", "")
	parent, child := startDelegatedChildCES(t, c)
	parent = waitForTaskStatus(t, c, parent.ID, core.TaskCompleted)
	child = waitForTaskStatus(t, c, child.ID, core.TaskCompleted)
	configs, _ := scriptRuntimeState(rt)
	if child.CESPolicy != core.CESEnforce || childFrames.Load() != 0 || len(configs) == 0 {
		t.Fatalf("workspace-on child = %#v, frames=%d phases=%d", child, childFrames.Load(), len(configs))
	}
}

func TestDelegatedChildPhaseAndArtifactFailuresFailChildAndReachParent(t *testing.T) {
	for _, test := range []struct {
		name    string
		scripts [][]string
	}{
		{name: "phase failure", scripts: nil},
		{name: "artifact failure", scripts: scriptsFor([]string{`{"unexpected":true}`})},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: test.scripts, started: make(chan *scriptSession, 10)}
			c, _ := newChildCESController(t, appQACConfig(), rt)
			_ = c.SetCESPolicy(CESPolicyOff)
			_, parentSawResult := configureChildCESDelegation(t, c, "enforce", "")
			_, child := startDelegatedChildCES(t, c)
			child = waitForTaskStatus(t, c, child.ID, core.TaskFailed)
			if child.FailureReason == "" {
				t.Fatalf("failed child has no reason: %#v", child)
			}
			waitFor(t, func() bool {
				for _, message := range c.WorkspaceSnapshot().Messages {
					if message.Type == core.MessageTaskResult && message.TaskID != nil && *message.TaskID == child.ID && strings.Contains(message.Content, child.FailureReason) {
						return true
					}
				}
				return false
			})
			if !parentSawResult.Load() {
				t.Fatal("parent did not receive the failed child result")
			}
		})
	}
}

func TestCancelParentCancelsChildCESPhase(t *testing.T) {
	gate := make(chan struct{})
	script := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10), gate: gate}
	rt := &childCancelRuntime{scriptRuntime: script, cancelled: make(chan struct{})}
	c, _ := newChildCESController(t, appQACConfig(), rt)
	_ = c.SetCESPolicy(CESPolicyOff)
	_, _ = configureChildCESDelegation(t, c, "enforce", "")
	parent, child := startDelegatedChildCES(t, c)
	select {
	case <-script.started:
	case <-time.After(5 * time.Second):
		t.Fatal("child phase did not start")
	}
	if err := c.CancelTask(parent.ID); err != nil {
		t.Fatal(err)
	}
	child = waitForTaskStatus(t, c, child.ID, core.TaskCancelled)
	select {
	case <-rt.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("child phase context was not cancelled")
	}
	configs, _ := scriptRuntimeState(script)
	if len(configs) != 1 {
		t.Fatalf("phase starts after cancellation = %d", len(configs))
	}
}

func TestChildCESDeadlineCancelsPhase(t *testing.T) {
	gate := make(chan struct{})
	script := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10), gate: gate}
	rt := &childCancelRuntime{scriptRuntime: script, cancelled: make(chan struct{})}
	c, _ := newChildCESController(t, appQACConfig(), rt)
	_ = c.SetCESPolicy(CESPolicyOff)
	_, _ = configureChildCESDelegation(t, c, "enforce", "1h")
	_, child := startDelegatedChildCES(t, c)
	select {
	case <-script.started:
	case <-time.After(5 * time.Second):
		t.Fatal("child phase did not start")
	}
	// The router belongs to the broker goroutine, so tick through a broker
	// command and wait outside the owner goroutine; ticking inline would
	// deadlock when the timeout result is queued for async delivery.
	deadline := *child.Limits.Budget.Deadline
	var tickDone <-chan error
	if err := c.call(func(o *owner) {
		tickDone = o.enqueueBroker(brokerCommand{apply: func(router *comms.Router) error {
			router.Tick(deadline.Add(time.Second))
			return nil
		}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-tickDone; err != nil {
		t.Fatal(err)
	}
	child = waitForTaskStatus(t, c, child.ID, core.TaskFailed)
	if child.FailureReason != "timeout" {
		t.Fatalf("child timeout reason = %q", child.FailureReason)
	}
	select {
	case <-rt.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("child deadline did not cancel its phase context")
	}
	configs, _ := scriptRuntimeState(script)
	if len(configs) != 1 {
		t.Fatalf("phase starts after deadline = %d", len(configs))
	}
}

func TestCloseCancelsChildCESPhase(t *testing.T) {
	gate := make(chan struct{})
	script := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10), gate: gate}
	rt := &childCancelRuntime{scriptRuntime: script, cancelled: make(chan struct{})}
	c, _ := newChildCESController(t, appQACConfig(), rt)
	_ = c.SetCESPolicy(CESPolicyOff)
	_, _ = configureChildCESDelegation(t, c, "enforce", "")
	_, _ = startDelegatedChildCES(t, c)
	select {
	case <-script.started:
	case <-time.After(5 * time.Second):
		t.Fatal("child phase did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case <-rt.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("controller close did not cancel the child phase context")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("controller close did not wait for the child worker")
	}
}

func TestEnforceChildWithoutPhaseRuntimeFailsAsUnavailable(t *testing.T) {
	c, _ := newChildCESController(t, appQACConfig(), nil)
	var phaseRuntime runtime.Runtime
	if err := c.call(func(o *owner) {
		phaseRuntime, o.phaseRuntime = o.phaseRuntime, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.call(func(o *owner) { o.phaseRuntime = phaseRuntime }) })
	_ = c.SetCESPolicy(CESPolicyOff)
	childFrames, _ := configureChildCESDelegation(t, c, "enforce", "")
	_, child := startDelegatedChildCES(t, c)
	child = waitForTaskStatus(t, c, child.ID, core.TaskFailed)
	if !strings.Contains(child.FailureReason, "ces_unavailable") || childFrames.Load() != 0 {
		t.Fatalf("unavailable child = %#v, persistent frames=%d", child, childFrames.Load())
	}
}

type childCESProgressVerifier struct{}

func (childCESProgressVerifier) VerifyProgress(epistemic.State, cognition.PhaseArtifact) cognition.ProgressEvidence {
	return cognition.ProgressEvidence{State: cognition.ProgressLow, Provenance: cognition.ProgressStructuredExecution, Strength: 1}
}

// The progress verifier feeds CES progress evidence and Jev triggering, so the
// owner must keep the option it was constructed with.
func TestNewKeepsProgressVerifierOption(t *testing.T) {
	c := New(config.Default(), nil, Options{Runtime: fake.NewRuntime(), Progress: childCESProgressVerifier{}})
	defer func() { _ = c.Close() }()
	var kept bool
	if err := c.call(func(o *owner) { _, kept = o.progressVerifier.(childCESProgressVerifier) }); err != nil {
		t.Fatal(err)
	}
	if !kept {
		t.Fatal("Options.Progress was not kept by the run owner")
	}
}
