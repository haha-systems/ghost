package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func newAutoDecisionService(t *testing.T, fallback string, timeout time.Duration, steps ...decision.FakeStep) (*decision.Service, *decision.FakeAgent) {
	t.Helper()
	service := decision.NewService(decision.ServiceConfig{
		Enabled:  map[core.DecisionKind]bool{core.DecisionKindCESMode: true},
		Fallback: fallback, Timeout: timeout,
	})
	agent := decision.NewFakeAgent(decision.Info{ID: "auto-test", Kinds: []core.DecisionKind{core.DecisionKindCESMode}}, steps...)
	if err := service.Register(agent); err != nil {
		t.Fatal(err)
	}
	return service, agent
}

func newCESAutoController(t *testing.T, cfg config.Config, rt runtime.Runtime, service *decision.Service, workdir string) (*Controller, <-chan Event) {
	t.Helper()
	c := New(cfg, nil, Options{Runtime: rt, Decisions: service, WorkDir: workdir})
	sessions := map[string]*fake.Session{"wraith": fake.NewSession("wraith"), "shade": fake.NewSession("shade")}
	installed := map[string]runtime.Session{"wraith": sessions["wraith"], "shade": sessions["shade"]}
	if err := c.SetSessions(installed); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := c.Subscribe()
	var sessionReaders sync.WaitGroup
	for _, session := range sessions {
		sessionReaders.Add(1)
		go func(session *fake.Session) {
			defer sessionReaders.Done()
			for item := range session.Events() {
				_ = c.Observe(item)
			}
		}(session)
	}
	t.Cleanup(func() {
		unsubscribe()
		if err := c.Close(); err != nil {
			t.Error(err)
		}
		for _, session := range sessions {
			_ = session.Close()
		}
		sessionReaders.Wait()
	})
	return c, events
}

func awaitAutoResolved(t *testing.T, events <-chan Event) Status {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case item, ok := <-events:
			if !ok {
				t.Fatal("run event stream closed")
			}
			if item.Boundary && item.Status != nil && !item.Status.CESAuto.Pending && item.Status.CESAuto.RequestID != "" && item.Status.CESAuto.Executed != "" {
				return *item.Status
			}
		case <-deadline.C:
			t.Fatal("auto decision did not resolve")
		}
	}
}

func awaitCoreEvent(t *testing.T, events <-chan Event, eventType string) core.Event {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case item, ok := <-events:
			if !ok {
				t.Fatal("run event stream closed")
			}
			if item.CoreEvent != nil && item.CoreEvent.Type == eventType {
				return *item.CoreEvent
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}

func TestCESAutoRecommendationOnRunsCESPhases(t *testing.T) {
	service, agent := newAutoDecisionService(t, "off", 0, decision.FakeStep{Response: core.DecisionResponse{Action: "ces_on", Uncertainty: .1}})
	rt := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10)}
	c, events := newCESAutoController(t, appQACConfig(), rt, service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("investigate and verify the event sequence"); err != nil {
		t.Fatal(err)
	}
	status := awaitAutoResolved(t, events)
	if status.CESAuto.Source != "decision" || status.CESAuto.Recommended != "ces_on" || status.CESAuto.Executed != "ces_on" || status.Effective != CESPolicyOn {
		t.Fatalf("auto CES selection = %#v", status)
	}
	if status, _ = terminalEvents(t, events); status.State.Task.Status != "complete" {
		t.Fatalf("CES task did not complete: %#v", status.State.Task)
	}
	if len(agent.Requests()) != 1 || len(c.Events(EventFilter{Type: "ces.phase"})) == 0 {
		t.Fatalf("requests=%d phases=%d", len(agent.Requests()), len(c.Events(EventFilter{Type: "ces.phase"})))
	}
	requested := c.Events(EventFilter{Type: "decision.requested"})[0]
	var request core.DecisionRequest
	if err := json.Unmarshal(requested.Payload, &request); err != nil || request.Kind != core.DecisionKindCESMode || len(request.Actions) != 2 {
		t.Fatalf("decision request = %#v, err=%v", request, err)
	}
	var state struct {
		Goal          string   `json:"goal"`
		Participants  []string `json:"participants"`
		OwnerResource string   `json:"owner_resource"`
		QACConfigured bool     `json:"qac_configured"`
	}
	if err := json.Unmarshal(request.State, &state); err != nil || state.Goal != "investigate and verify the event sequence" || state.OwnerResource == "" || !state.QACConfigured {
		t.Fatalf("decision state = %#v, err=%v", state, err)
	}
	recommended := c.Events(EventFilter{Type: "decision.recommended"})[0]
	applied := c.Events(EventFilter{Type: "decision.applied"})[0]
	if recommended.ID == applied.ID || recommended.Seq >= applied.Seq || applied.CorrelationID != request.ID {
		t.Fatalf("recommendation and application were not separate ordered events: %#v %#v", recommended, applied)
	}
}

func TestCESAutoRecommendationOffRunsStandardPath(t *testing.T) {
	service, agent := newAutoDecisionService(t, "on", 0, decision.FakeStep{Response: core.DecisionResponse{Action: "ces_off", Uncertainty: .2}})
	c, events := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("write one clear label"); err != nil {
		t.Fatal(err)
	}
	status := awaitAutoResolved(t, events)
	if status.CESAuto.Source != "decision" || status.CESAuto.Executed != "ces_off" || status.Effective != CESPolicyOff {
		t.Fatalf("auto standard selection = %#v", status)
	}
	if tasks := c.WorkspaceSnapshot().Tasks; len(tasks) != 1 || tasks[0].CESPolicy != core.CESOff {
		t.Fatalf("standard task = %#v", tasks)
	}
	if len(c.Events(EventFilter{Type: "ces.phase"})) != 0 || len(agent.Requests()) != 1 {
		t.Fatalf("phases=%d decision requests=%d", len(c.Events(EventFilter{Type: "ces.phase"})), len(agent.Requests()))
	}
	_ = c.Cancel()
}

func TestCESAutoDecisionFailuresUseOffFallback(t *testing.T) {
	for _, test := range []struct {
		name    string
		step    decision.FakeStep
		timeout time.Duration
	}{
		{name: "agent error", step: decision.FakeStep{Err: errors.New("agent failed")}},
		{name: "timeout", step: decision.FakeStep{Release: make(chan struct{})}, timeout: 20 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newAutoDecisionService(t, "off", test.timeout, test.step)
			c, events := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
			if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
				t.Fatal(err)
			}
			if err := c.Start("make a small change"); err != nil {
				t.Fatal(err)
			}
			status := awaitAutoResolved(t, events)
			if status.CESAuto.Source != "fallback" || status.CESAuto.Executed != "ces_off" || status.CESAuto.Reason == "" {
				t.Fatalf("fallback status = %#v", status.CESAuto)
			}
			applied := c.Events(EventFilter{Type: "decision.applied"})
			var execution struct {
				Action string `json:"action"`
				Source string `json:"source"`
				Reason string `json:"reason"`
			}
			if len(applied) == 1 {
				_ = json.Unmarshal(applied[0].Payload, &execution)
			}
			if len(applied) != 1 || execution.Action != "ces_off" || execution.Source != "fallback" || execution.Reason == "" || len(c.WorkspaceSnapshot().Tasks) != 1 {
				t.Fatal("fallback did not record and start standard work")
			}
			_ = c.Cancel()
		})
	}
}

func TestCESAutoDecisionBudgetExhaustedUsesFallback(t *testing.T) {
	service, agent := newAutoDecisionService(t, "off", 0, decision.FakeStep{Response: core.DecisionResponse{Action: "ces_on", Uncertainty: .1}})
	service.SetLimits(1, 1)
	consumed := core.DecisionRequest{
		ID: "event_00000000000000010000000000000041", Kind: core.DecisionKindCESMode,
		TaskID: "task_00000000000000010000000000000041", RequestingAgent: "agent_00000000000000010000000000000041",
		Actions: []string{"ces_on", "ces_off"},
	}
	if outcome := service.Decide(context.Background(), consumed); outcome.Status != decision.StatusRecommended {
		t.Fatalf("budget setup outcome = %#v", outcome)
	}
	c, events := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("request work after budget is spent"); err != nil {
		t.Fatal(err)
	}
	status := awaitAutoResolved(t, events)
	if status.CESAuto.Source != "fallback" || status.CESAuto.Executed != "ces_off" || !strings.Contains(status.CESAuto.Reason, "budget") {
		t.Fatalf("budget fallback = %#v", status.CESAuto)
	}
	if len(c.Events(EventFilter{Type: "decision.budget_exhausted"})) != 1 || len(agent.Requests()) != 1 {
		t.Fatal("budget exhaustion was not recorded without a second agent call")
	}
	_ = c.Cancel()
}

func TestStartWithManualPolicySkipsCESDecisionAgent(t *testing.T) {
	service, agent := newAutoDecisionService(t, "off", 0, decision.FakeStep{Response: core.DecisionResponse{Action: "ces_on", Uncertainty: 0}})
	c, _ := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.StartWithPolicy("manual standard task", CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if len(agent.Requests()) != 0 || c.Status().CESAuto.Pending {
		t.Fatal("manual start requested an automatic CES decision")
	}
	_ = c.Cancel()
}

func TestCESAutoWithoutQACUsesOffAndExplainsWhy(t *testing.T) {
	service, agent := newAutoDecisionService(t, "on", 0, decision.FakeStep{Response: core.DecisionResponse{Action: "ces_on", Uncertainty: 0}})
	cfg := appQACConfig()
	cfg.QAC = config.QACConfig{}
	c, events := newCESAutoController(t, cfg, fake.NewRuntime(), service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	status := c.Status()
	if status.Requested != CESPolicyAuto || status.Effective != CESPolicyOff || status.PolicyReason != "unsupported: QAC not configured" {
		t.Fatalf("unsupported auto policy = %#v", status)
	}
	if err := c.Start("run without QAC"); err != nil {
		t.Fatal(err)
	}
	if len(agent.Requests()) != 0 || len(c.WorkspaceSnapshot().Tasks) != 1 || c.Status().CESAuto.Reason != "unsupported: QAC not configured" {
		t.Fatalf("unsupported auto did not use standard work with a reason: status=%#v tasks=%#v requests=%#v", c.Status(), c.WorkspaceSnapshot().Tasks, agent.Requests())
	}
	_ = events
	_ = c.Cancel()
}

func TestCESAutoWithoutDecisionServiceUsesConfiguredFallback(t *testing.T) {
	cfg := appQACConfig()
	cfg.Decisions.Fallback = "off"
	c, _ := newCESAutoController(t, cfg, fake.NewRuntime(), nil, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("run with no decision service"); err != nil {
		t.Fatal(err)
	}
	if status := c.Status(); status.CESAuto.Source != "fallback" || status.CESAuto.Executed != "ces_off" || status.CESAuto.Reason == "" {
		t.Fatalf("missing decision service fallback = %#v", status.CESAuto)
	}
	if len(c.Events(EventFilter{Type: "decision.unavailable"})) != 1 || len(c.Events(EventFilter{Type: "decision.applied"})) != 1 {
		t.Fatal("missing decision service was not recorded")
	}
	_ = c.Cancel()
}

func TestDelegatedChildInheritsPolicyAndDoesNotRequestCESDecision(t *testing.T) {
	service, agent := newAutoDecisionService(t, "off", 0, decision.FakeStep{Response: core.DecisionResponse{Action: "ces_off", Uncertainty: .1}})
	c, events := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
	var parentSends int
	if err := c.call(func(o *owner) {
		parent := o.sessions["wraith"].(*fake.Session)
		child := o.sessions["shade"].(*fake.Session)
		parent.OnSend = func(input runtime.Input) []runtime.Event {
			parentSends++
			if parentSends != 1 {
				return channelEvent("wraith", "the delegated work is complete")
			}
			parentID := frameValue(input.Text, "task_id")
			block := "```ghost-delegate\n" + `{"schema_version":1,"target":"shade","parent_task_id":"` + parentID + `","objective":"check the result","context_refs":[],"expected_deliverable":"one finding"}` + "\n```"
			return channelEvent("wraith", block)
		}
		child.OnSend = func(input runtime.Input) []runtime.Event {
			childID := frameValue(input.Text, "task_id")
			block := "```ghost-result\n" + `{"schema_version":1,"task_id":"` + childID + `","status":"completed","result":"one finding"}` + "\n```"
			return channelEvent("shade", block)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Send([]string{"wraith"}, "review the event result"); err != nil {
		t.Fatal(err)
	}
	_ = awaitAutoResolved(t, events)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		workspace := c.WorkspaceSnapshot()
		if len(workspace.Tasks) == 2 && workspace.Tasks[0].Status == core.TaskCompleted && workspace.Tasks[1].Status == core.TaskCompleted {
			if workspace.Tasks[0].CESPolicy != core.CESOff || workspace.Tasks[1].CESPolicy != workspace.Tasks[0].CESPolicy {
				t.Fatalf("child policy was not inherited: %#v", workspace.Tasks)
			}
			break
		}
		select {
		case _, ok := <-events:
			if !ok {
				t.Fatal("run event stream closed")
			}
		case <-deadline.C:
			t.Fatalf("delegated child did not complete: tasks=%#v messages=%#v events=%#v", workspace.Tasks, workspace.Messages, c.Events(EventFilter{Category: core.CategoryConversation}))
		}
	}
	if len(agent.Requests()) != 1 || len(c.Events(EventFilter{Type: "ces.phase"})) != 0 {
		t.Fatalf("child requested an automatic CES decision: requests=%d phases=%d", len(agent.Requests()), len(c.Events(EventFilter{Type: "ces.phase"})))
	}
}

func TestOverrideCESDecisionRunsOperatorChoiceAndIgnoresLateRecommendation(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	service, _ := newAutoDecisionService(t, "off", 0, decision.FakeStep{
		Response: core.DecisionResponse{Action: "ces_on", Uncertainty: .1}, Started: started, Release: release,
	})
	c, events := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("operator overrides automatic choice"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("decision agent did not start")
	}
	requestID := c.Status().CESAuto.RequestID
	if err := c.Start("a second start while the decision is pending"); err == nil || err.Error() != "work is active" {
		t.Fatalf("second Start while pending = %v", err)
	}
	if err := c.OverrideDecision(requestID, "ces_off", "use the standard path"); err != nil {
		t.Fatal(err)
	}
	status := c.Status()
	if status.CESAuto.Pending || status.CESAuto.Source != "override" || status.CESAuto.Executed != "ces_off" || len(c.WorkspaceSnapshot().Tasks) != 1 {
		t.Fatalf("operator override = %#v", status.CESAuto)
	}
	close(release)
	recommended := awaitCoreEvent(t, events, "decision.recommended")
	ignored := awaitCoreEvent(t, events, "decision.ignored")
	if recommended.CorrelationID != requestID || ignored.CorrelationID != requestID || !strings.Contains(string(ignored.Payload), "superseded by operator override") {
		t.Fatalf("late recommendation handling = %#v %#v", recommended, ignored)
	}
	overridden := c.Events(EventFilter{Type: "decision.overridden"})
	if len(overridden) != 1 || overridden[0].Actor.Kind != core.ActorOperator {
		t.Fatalf("override event = %#v", overridden)
	}
	if status := c.Status(); status.Requested != CESPolicyAuto || status.Effective != CESPolicyOff {
		t.Fatalf("manual override changed the requested workspace policy: %#v", status)
	}
	_ = c.Cancel()
}

func TestSetCESPolicyOnOverridesPendingAutoDecision(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	service, _ := newAutoDecisionService(t, "off", 0, decision.FakeStep{
		Response: core.DecisionResponse{Action: "ces_off", Uncertainty: .2}, Started: started, Release: release,
	})
	gate := make(chan struct{})
	rt := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10), gate: gate}
	c, events := newCESAutoController(t, appQACConfig(), rt, service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("manual policy takes precedence"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("decision agent did not start")
	}
	if err := c.SetCESPolicy(CESPolicyOn); err != nil {
		t.Fatal(err)
	}
	if status := c.Status(); status.CESAuto.Pending || status.Requested != CESPolicyOn || status.Effective != CESPolicyOn || status.CESAuto.Executed != "ces_on" {
		t.Fatalf("manual policy override = %#v", status)
	}
	if tasks := c.WorkspaceSnapshot().Tasks; len(tasks) != 1 || tasks[0].CESPolicy != core.CESEnforce {
		t.Fatalf("manual policy did not start CES: %#v", tasks)
	}
	close(release)
	_ = awaitCoreEvent(t, events, "decision.recommended")
	ignored := awaitCoreEvent(t, events, "decision.ignored")
	if !strings.Contains(string(ignored.Payload), "superseded by operator override") {
		t.Fatalf("late recommendation was not ignored: %s", ignored.Payload)
	}
	_ = c.Cancel()
}

func TestCESAutoResumesAfterManualOverride(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	service, agent := newAutoDecisionService(t, "off", 0,
		decision.FakeStep{Response: core.DecisionResponse{Action: "ces_on", Uncertainty: .1}, Started: started, Release: release},
		decision.FakeStep{Response: core.DecisionResponse{Action: "ces_off", Uncertainty: .2}},
	)
	c, events := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("first automatic task"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first decision agent call did not start")
	}
	requestID := c.Status().CESAuto.RequestID
	if err := c.OverrideDecision(requestID, "ces_off", "operator selected standard mode"); err != nil {
		t.Fatal(err)
	}
	close(release)
	_ = awaitCoreEvent(t, events, "decision.ignored")
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("automatic mode resumes"); err != nil {
		t.Fatal(err)
	}
	_ = awaitCoreEvent(t, events, "decision.recommended")
	if len(agent.Requests()) != 2 {
		t.Fatalf("automatic decisions after manual override = %d, want 2", len(agent.Requests()))
	}
	status := c.Status()
	if status.Requested != CESPolicyAuto || status.CESAuto.Pending || status.CESAuto.Executed != "ces_off" || status.CESAuto.Source != "decision" {
		t.Fatalf("automatic mode did not resume: %#v", status)
	}
	_ = c.Cancel()
}

func TestCancelPendingCESAutoStartRecordsIgnoredAndStartsNothing(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	service, _ := newAutoDecisionService(t, "off", 0, decision.FakeStep{
		Response: core.DecisionResponse{Action: "ces_on", Uncertainty: .1}, Started: started, Release: release,
	})
	c, events := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("cancel the automatic start"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("decision agent did not start")
	}
	requestID := c.Status().CESAuto.RequestID
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	status := c.Status()
	if status.Active || status.CESAuto.Pending || len(c.WorkspaceSnapshot().Tasks) != 0 {
		t.Fatalf("cancelled pending start left work active: %#v", status)
	}
	ignored := c.Events(EventFilter{Type: "decision.ignored"})
	if len(ignored) != 1 || ignored[0].CorrelationID != requestID || !strings.Contains(string(ignored[0].Payload), "cancelled") || len(c.Events(EventFilter{Type: "decision.applied"})) != 0 {
		t.Fatalf("cancel record = %#v", ignored)
	}
	close(release)
	_ = awaitCoreEvent(t, events, "decision.unavailable")
	if len(c.WorkspaceSnapshot().Tasks) != 0 || c.Status().Active {
		t.Fatal("late result started work after cancellation")
	}
}

func TestCESAutoRecommendationOnFallsBackWhenQACBecomesUnavailable(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	service, _ := newAutoDecisionService(t, "on", 0, decision.FakeStep{
		Response: core.DecisionResponse{Action: "ces_on", Uncertainty: .1}, Started: started, Release: release,
	})
	c, events := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("QAC may become unavailable"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("decision agent did not start")
	}
	if err := c.call(func(o *owner) { o.cognition = nil }); err != nil {
		t.Fatal(err)
	}
	close(release)
	status := awaitAutoResolved(t, events)
	if status.CESAuto.Recommended != "ces_on" || status.CESAuto.Executed != "ces_off" || status.CESAuto.Source != "fallback" || status.CESAuto.Reason != "provider/QAC incompatible" {
		t.Fatalf("incompatible recommendation fallback = %#v", status.CESAuto)
	}
	ignored := c.Events(EventFilter{Type: "decision.ignored"})
	if len(ignored) != 1 || !strings.Contains(string(ignored[0].Payload), "provider/QAC incompatible") || len(c.Events(EventFilter{Type: "decision.applied"})) != 1 || len(c.Events(EventFilter{Type: "ces.phase"})) != 0 {
		t.Fatal("incompatible CES recommendation did not fall back safely")
	}
	_ = c.Cancel()
}

func TestCESAutoPendingDecisionIsInterruptedOnRecovery(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	service, _ := newAutoDecisionService(t, "off", 0, decision.FakeStep{
		Response: core.DecisionResponse{Action: "ces_on", Uncertainty: .1}, Started: started, Release: release,
	})
	workdir := t.TempDir()
	c, _ := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), service, workdir)
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("interrupt this pending decision"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("decision agent did not start")
	}
	requestID := c.Status().CESAuto.RequestID
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := New(config.Default(), nil, Options{WorkDir: workdir})
	t.Cleanup(func() { _ = restarted.Close() })
	ignored := restarted.Events(EventFilter{Type: "decision.ignored"})
	if len(ignored) != 1 || ignored[0].CorrelationID != requestID || !strings.Contains(string(ignored[0].Payload), "interrupted") {
		t.Fatalf("recovery interruption = %#v", ignored)
	}
	if len(restarted.WorkspaceSnapshot().Tasks) != 0 || len(restarted.Events(EventFilter{Type: "decision.requested"})) != 1 {
		t.Fatal("recovery resumed work or created another decision request")
	}
	close(release)
}

func TestCESPolicyOffToOnWaitsForCurrentProviderTurn(t *testing.T) {
	c, _ := newCESAutoController(t, appQACConfig(), fake.NewRuntime(), nil, "")
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("finish the current standard turn first"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCESPolicy(CESPolicyOn); err != nil {
		t.Fatal(err)
	}
	status := c.Status()
	if status.Requested != CESPolicyOn || status.Effective != CESPolicyOff || !status.Pending {
		t.Fatalf("policy changed during provider turn: %#v", status)
	}
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("apply CES at the next boundary"); err != nil {
		t.Fatal(err)
	}
	if status = c.Status(); status.Effective != CESPolicyOn || status.Pending || !status.Active {
		t.Fatalf("pending CES policy did not apply: %#v", status)
	}
	_ = c.Cancel()
}

func TestCESPolicyOnToAutoWaitsForCurrentProviderTurn(t *testing.T) {
	started := make(chan struct{}, 1)
	service, agent := newAutoDecisionService(t, "off", 0, decision.FakeStep{Response: core.DecisionResponse{Action: "ces_off", Uncertainty: 0}, Started: started})
	gate := make(chan struct{})
	rt := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10), gate: gate}
	c, _ := newCESAutoController(t, appQACConfig(), rt, service, "")
	if err := c.Start("keep CES active"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	status := c.Status()
	if status.Requested != CESPolicyAuto || status.Effective != CESPolicyOn || !status.Pending || len(agent.Requests()) != 0 {
		t.Fatalf("auto policy switched during provider turn: %#v requests=%d", status, len(agent.Requests()))
	}
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("apply auto at the next boundary"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("pending auto policy did not request a decision")
	}
	if len(agent.Requests()) != 1 {
		t.Fatalf("decision requests = %d", len(agent.Requests()))
	}
	close(gate)
	_ = c.Cancel()
}

func TestCESPolicyAutoToOffWaitsForCurrentProviderTurn(t *testing.T) {
	service, _ := newAutoDecisionService(t, "off", 0, decision.FakeStep{Response: core.DecisionResponse{Action: "ces_on", Uncertainty: 0}})
	gate := make(chan struct{})
	rt := &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scriptsFor(chainArtifacts), started: make(chan *scriptSession, 10), gate: gate}
	c, events := newCESAutoController(t, appQACConfig(), rt, service, "")
	if err := c.SetCESPolicy(CESPolicyAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("automatic CES run with a live phase"); err != nil {
		t.Fatal(err)
	}
	_ = awaitAutoResolved(t, events)
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	status := c.Status()
	if status.Requested != CESPolicyOff || status.Effective != CESPolicyOn || !status.Pending {
		t.Fatalf("off policy switched during provider turn: %#v", status)
	}
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("apply off at the next boundary"); err != nil {
		t.Fatal(err)
	}
	if status = c.Status(); status.Effective != CESPolicyOff || status.Pending || status.CESAuto.Pending || len(c.Events(EventFilter{Type: "decision.requested"})) != 1 {
		t.Fatalf("pending off policy did not apply: %#v", status)
	}
	_ = c.Cancel()
}
