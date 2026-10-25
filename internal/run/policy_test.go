package run

import (
	"reflect"
	"testing"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/runtime"
)

func TestCESPolicyDefaultAndUnsupportedRequest(t *testing.T) {
	c, _, _ := newScript(t, []string{})
	defer c.Close()
	status := c.Status()
	if status.Requested != CESPolicyOn || status.Effective != CESPolicyOn || status.Pending {
		t.Fatalf("configured default policy = %#v", status)
	}
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if status = c.Status(); status.Requested != CESPolicyOff || status.Effective != CESPolicyOff || status.Pending {
		t.Fatalf("immediate policy change = %#v", status)
	}

	unsupported := New(config.Default(), nil, Options{})
	defer unsupported.Close()
	if err := unsupported.SetCESPolicy(CESPolicyOn); err != nil {
		t.Fatal(err)
	}
	status = unsupported.Status()
	if status.Requested != CESPolicyOn || status.Effective != CESPolicyOff || status.Pending || status.PolicyReason != "unsupported: QAC not configured" {
		t.Fatalf("unsupported policy = %#v", status)
	}
	if len(unsupported.Events(EventFilter{Type: "ces.policy_changed"})) == 0 {
		t.Fatal("policy change event was not recorded")
	}
}

func TestCESPolicyOffUsesPersistentSessionsWithoutCESPhases(t *testing.T) {
	c, phaseRuntime, _ := newScript(t, []string{})
	defer c.Close()
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("run without CES"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		active := 0
		_ = c.call(func(o *owner) {
			for _, session := range o.sessions {
				if session.State() == runtime.StateRunning {
					active++
				}
			}
		})
		return active == 2
	})
	phaseRuntime.mu.Lock()
	phaseStarts := len(phaseRuntime.configs)
	phaseRuntime.mu.Unlock()
	if phaseStarts != 0 {
		t.Fatalf("phase runtime started %d times with CES off", phaseStarts)
	}
	status := c.Status()
	if !status.Active || status.Effective != CESPolicyOff {
		t.Fatalf("non-CES run status = %#v", status)
	}
	workspace := c.WorkspaceSnapshot()
	if len(workspace.Tasks) != 1 || workspace.Tasks[0].Status != core.TaskQueued || workspace.Tasks[0].CESPolicy != core.CESOff {
		t.Fatalf("non-CES task = %#v", workspace.Tasks)
	}
	if len(c.Events(EventFilter{Type: "ces.phase"})) != 0 {
		t.Fatal("CES phase event appeared with the policy off")
	}
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	if got := c.WorkspaceSnapshot().Tasks[0].Status; got != core.TaskCancelled {
		t.Fatalf("cancelled standard task status = %s", got)
	}
}

func TestCESPolicyChangeDuringPhaseWaitsForNextStartAndKeepsState(t *testing.T) {
	c, phaseRuntime, events := newScript(t, []string{})
	defer c.Close()
	if err := c.Start("hold the current phase"); err != nil {
		t.Fatal(err)
	}
	for {
		item := nextEvent(t, events)
		if item.Phase != nil && item.Phase.Name == cognition.PhaseTurnRequested {
			break
		}
	}
	before := c.Status().State
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	status := c.Status()
	if status.Requested != CESPolicyOff || status.Effective != CESPolicyOn || !status.Pending || status.State.Task.ID != before.Task.ID || !status.Active {
		t.Fatalf("mid-phase policy status = %#v", status)
	}
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	status, _ = terminalEvents(t, events)
	stateAfterCancel := status.State
	if !c.Status().Pending {
		t.Fatal("pending policy applied before the next Start")
	}
	if err := c.Start("next non-CES run"); err != nil {
		t.Fatal(err)
	}
	status = c.Status()
	if status.Requested != CESPolicyOff || status.Effective != CESPolicyOff || status.Pending || !reflect.DeepEqual(status.State, stateAfterCancel) {
		t.Fatalf("next Start did not apply policy or preserve CES state: %#v", status)
	}
	phaseRuntime.mu.Lock()
	phaseStarts := len(phaseRuntime.configs)
	phaseRuntime.mu.Unlock()
	if phaseStarts != 1 {
		t.Fatalf("policy-off Start ran an extra CES phase; starts=%d", phaseStarts)
	}
	_ = c.Cancel()
}

func TestStartWithPolicyOverridesGlobalDefault(t *testing.T) {
	c, phaseRuntime, _ := newScript(t, []string{})
	defer c.Close()
	if err := c.StartWithPolicy("direct task", CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if tasks := c.WorkspaceSnapshot().Tasks; len(tasks) != 1 || tasks[0].CESPolicy != core.CESOff {
		t.Fatalf("task override off = %#v", tasks)
	}
	phaseRuntime.mu.Lock()
	starts := len(phaseRuntime.configs)
	phaseRuntime.mu.Unlock()
	if starts != 0 || c.Status().Effective != CESPolicyOn {
		t.Fatalf("task override changed global policy or ran CES: starts=%d status=%#v", starts, c.Status())
	}
}

func TestStartWithPolicyOnRunsCESWithoutChangingGlobalPolicy(t *testing.T) {
	c, phaseRuntime, events := newScript(t, []string{})
	defer c.Close()
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.StartWithPolicy("task override on", CESPolicyOn); err != nil {
		t.Fatal(err)
	}
	if tasks := c.WorkspaceSnapshot().Tasks; len(tasks) != 1 || tasks[0].CESPolicy != core.CESEnforce {
		t.Fatalf("task override on = %#v", tasks)
	}
	if c.Status().Effective != CESPolicyOff {
		t.Fatal("task override changed the global CES policy")
	}
	for {
		if item := nextEvent(t, events); item.Phase != nil {
			break
		}
	}
	waitFor(t, func() bool {
		phaseRuntime.mu.Lock()
		defer phaseRuntime.mu.Unlock()
		return len(phaseRuntime.configs) == 1
	})
	phaseRuntime.mu.Lock()
	starts := len(phaseRuntime.configs)
	phaseRuntime.mu.Unlock()
	if starts != 1 {
		t.Fatalf("task override did not run one CES phase: %d", starts)
	}
	_ = c.Cancel()
}
