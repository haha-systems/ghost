package comms

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

type testDirectory struct {
	mu      sync.RWMutex
	agents  map[core.AgentID]core.Agent
	blocked map[string]bool
}

func (d *testDirectory) Agent(id core.AgentID) (core.Agent, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	a, ok := d.agents[id]
	return a, ok
}

func (d *testDirectory) Agents() []core.Agent {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]core.Agent, 0, len(d.agents))
	for _, agent := range d.agents {
		out = append(out, agent)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (d *testDirectory) CanMessage(from, to core.AgentID) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return !d.blocked[pairKey(from, to)]
}

func (d *testDirectory) setAvailability(id core.AgentID, availability core.Availability) {
	d.mu.Lock()
	defer d.mu.Unlock()
	agent := d.agents[id]
	agent.Availability = availability
	d.agents[id] = agent
}

type testDelivery struct {
	mu    sync.Mutex
	calls []deliveryCall
	errs  map[core.AgentID]error
}

type deliveryCall struct {
	to      core.AgentID
	message core.Message
}

func (d *testDelivery) Deliver(_ context.Context, to core.AgentID, message core.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, deliveryCall{to: to, message: cloneTestMessage(message)})
	return d.errs[to]
}

func (d *testDelivery) snapshot() []deliveryCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]deliveryCall(nil), d.calls...)
}

type testClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *testClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

type routerFixture struct {
	router    *Router
	clock     *testClock
	directory *testDirectory
	delivery  *testDelivery
	limits    Limits
}

func newRouterFixture(t *testing.T, limits Limits) routerFixture {
	t.Helper()
	clock := &testClock{now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	a, b, c := testAgent(1), testAgent(2), testAgent(3)
	directory := &testDirectory{agents: map[core.AgentID]core.Agent{
		a.ID: a, b.ID: b, c.ID: c,
	}, blocked: map[string]bool{}}
	delivery := &testDelivery{errs: map[core.AgentID]error{}}
	router, err := NewRouter(RouterConfig{
		WorkspaceID: core.WorkspaceID(testID("workspace", 1)),
		Clock:       clock.Now,
		Directory:   directory,
		Delivery:    delivery,
		Limits:      limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	return routerFixture{router: router, clock: clock, directory: directory, delivery: delivery, limits: limits}
}

func testAgent(n int) core.Agent {
	return core.Agent{
		ID: core.AgentID(testID("agent", n)), DisplayName: fmt.Sprintf("Agent %d", n),
		Runtime: core.RuntimeCodex, Availability: core.AvailabilityAvailable,
		PermissionPolicy: core.PermissionPolicy{Mode: core.PermissionModeInherit},
	}
}

func testID(prefix string, n int) string { return fmt.Sprintf("%s_%032x", prefix, n) }

func (f routerFixture) registerParent(t *testing.T, owner core.AgentID, limits core.TaskLimits) core.Task {
	t.Helper()
	now := f.clock.Now()
	task := core.Task{
		ID: core.TaskID(testID("task", 100)), Requester: owner, Owner: owner,
		Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now,
		CESPolicy: core.CESEnforce, ThreadID: core.ThreadID(testID("thread", 100)),
		Limits:           limits,
		PermissionPolicy: &core.PermissionPolicy{Mode: core.PermissionModeAllowList, AllowTools: []string{"read", "write"}},
	}
	if err := f.router.RegisterTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

func basicMessage(sender, recipient core.AgentID) core.Message {
	return core.Message{
		Sender: sender, Recipients: []core.AgentID{recipient},
		ThreadID: core.ThreadID(testID("thread", 1)), Type: core.MessageDirect,
		Content: "hello",
	}
}

func baseDelegation(parent core.Task, requester, target core.AgentID) DelegationRequest {
	return DelegationRequest{
		Requester: requester, Target: target, ParentTaskID: parent.ID,
		Objective: "inspect the parser", ExpectedDeliverable: "findings",
	}
}

func TestSendDirectPreservesSenderAndRecipient(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	message, err := f.router.Send(context.Background(), basicMessage(testAgent(1).ID, testAgent(2).ID))
	if err != nil {
		t.Fatal(err)
	}
	calls := f.delivery.snapshot()
	if len(calls) != 1 || calls[0].to != testAgent(2).ID {
		t.Fatalf("deliveries = %#v", calls)
	}
	if calls[0].message.Sender != testAgent(1).ID || !reflect.DeepEqual(calls[0].message.Recipients, []core.AgentID{testAgent(2).ID}) {
		t.Fatalf("delivery changed message route: %#v", calls[0].message)
	}
	if message.ID == "" || message.RecipientDelivery[testAgent(2).ID] != core.DeliveryDelivered {
		t.Fatalf("message was not accepted and delivered: %#v", message)
	}
	if got := eventTypes(f.router.Snapshot().Events); !containsAll(got, "message.accepted", "message.delivered") {
		t.Fatalf("events = %v", got)
	}
	events := f.router.Snapshot().Events
	var accepted core.Event
	for _, item := range events {
		if item.Type == "message.accepted" {
			accepted = item
		}
		if item.Type == "message.delivered" && (item.CorrelationID != accepted.CorrelationID || item.CausationID != accepted.ID) {
			t.Fatalf("delivery event lost causal links: accepted=%#v delivered=%#v", accepted, item)
		}
	}
}

func TestSendRejectsUnknownAndUnauthorizedRoutes(t *testing.T) {
	tests := []struct {
		name    string
		sender  core.AgentID
		target  core.AgentID
		blocked bool
	}{
		{name: "unknown sender", sender: "bad", target: testAgent(2).ID},
		{name: "unknown recipient", sender: testAgent(1).ID, target: core.AgentID(testID("agent", 99))},
		{name: "permission denied", sender: testAgent(1).ID, target: testAgent(2).ID, blocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRouterFixture(t, Limits{})
			if tt.blocked {
				f.directory.blocked[pairKey(tt.sender, tt.target)] = true
			}
			if _, err := f.router.Send(context.Background(), basicMessage(tt.sender, tt.target)); err == nil {
				t.Fatal("invalid route was accepted")
			}
			snapshot := f.router.Snapshot()
			if len(snapshot.Messages) != 0 || !containsAll(eventTypes(snapshot.Events), "message.rejected") {
				t.Fatalf("rejection was not recorded: %#v", snapshot)
			}
		})
	}
}

func TestBroadcastUsesOneMessageAndPerRecipientState(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	message, err := f.router.Send(context.Background(), core.Message{
		Sender: testAgent(1).ID, ThreadID: core.ThreadID(testID("thread", 2)),
		Type: core.MessageBroadcast, Content: "status",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Recipients) != 2 || len(message.RecipientDelivery) != 2 || len(f.delivery.snapshot()) != 2 {
		t.Fatalf("broadcast did not fan out: %#v calls=%d", message, len(f.delivery.snapshot()))
	}
	for _, recipient := range message.Recipients {
		if message.RecipientDelivery[recipient] != core.DeliveryDelivered {
			t.Errorf("recipient %s state = %s", recipient, message.RecipientDelivery[recipient])
		}
	}
}

func TestDelegationInheritsCESAndIntersectsPermissions(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	a, b := testAgent(1), testAgent(2)
	a.PermissionPolicy = core.PermissionPolicy{Mode: core.PermissionModeAllowList, AllowTools: []string{"read", "write"}}
	b.PermissionPolicy = core.PermissionPolicy{Mode: core.PermissionModeAllowList, AllowTools: []string{"read", "write", "shell"}}
	f.directory.agents[a.ID], f.directory.agents[b.ID] = a, b
	parent := f.registerParent(t, a.ID, core.TaskLimits{})
	child, err := f.router.Delegate(context.Background(), baseDelegation(parent, a.ID, b.ID))
	if err != nil {
		t.Fatal(err)
	}
	if child.Parent == nil || *child.Parent != parent.ID || child.Depth != 1 || child.CESPolicy != parent.CESPolicy {
		t.Fatalf("child did not inherit task contract: %#v", child)
	}
	if child.PermissionPolicy == nil || child.PermissionPolicy.Mode != core.PermissionModeAllowList || !reflect.DeepEqual(child.PermissionPolicy.AllowTools, []string{"read", "write"}) {
		t.Fatalf("child permissions were not intersected: %#v", child.PermissionPolicy)
	}
}

func TestCompleteReturnsTaskResultToRequester(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	parent := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
	child, err := f.router.Delegate(context.Background(), baseDelegation(parent, testAgent(1).ID, testAgent(2).ID))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := f.router.Complete(context.Background(), child.ID, "review is clear")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != core.TaskCompleted {
		t.Fatalf("task status = %s", completed.Status)
	}
	snapshot := f.router.Snapshot()
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d, want delegation and result", len(snapshot.Messages))
	}
	result := snapshot.Messages[1]
	if result.Type != core.MessageTaskResult || result.Sender != testAgent(2).ID || !reflect.DeepEqual(result.Recipients, []core.AgentID{testAgent(1).ID}) || result.Content != "review is clear" {
		t.Fatalf("result message = %#v", result)
	}
}

func TestCancelCascadesToOpenDescendants(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	root := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
	child, err := f.router.Delegate(context.Background(), baseDelegation(root, testAgent(1).ID, testAgent(2).ID))
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := f.router.Delegate(context.Background(), baseDelegation(child, testAgent(2).ID, testAgent(3).ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.router.Cancel(context.Background(), child.ID, "operator stop"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []core.TaskID{child.ID, grandchild.ID} {
		task, ok := f.router.Snapshot().Task(id)
		if !ok || task.Status != core.TaskCancelled {
			t.Errorf("task %s = %#v, found=%v", id, task, ok)
		}
	}
}

func TestTickTimesOutDelegationAndRecordsReason(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	parent := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
	req := baseDelegation(parent, testAgent(1).ID, testAgent(2).ID)
	req.Timeout = time.Minute
	child, err := f.router.Delegate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	now := f.clock.Now().Add(time.Minute)
	f.clock.Set(now)
	f.router.Tick(now)
	timedOut, ok := f.router.Snapshot().Task(child.ID)
	if !ok || timedOut.Status != core.TaskFailed || timedOut.FailureReason != "timeout" {
		t.Fatalf("timed out task = %#v, found=%v", timedOut, ok)
	}
}

func TestDepthAndAgentCycleLimitsRejectUnsafeDelegation(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		f := newRouterFixture(t, Limits{MaxDelegationDepth: 1})
		root := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
		child, err := f.router.Delegate(context.Background(), baseDelegation(root, testAgent(1).ID, testAgent(2).ID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.router.Delegate(context.Background(), baseDelegation(child, testAgent(2).ID, testAgent(3).ID)); err == nil {
			t.Fatal("delegation beyond configured depth succeeded")
		}
	})
	t.Run("cycle", func(t *testing.T) {
		f := newRouterFixture(t, Limits{})
		root := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
		child, err := f.router.Delegate(context.Background(), baseDelegation(root, testAgent(1).ID, testAgent(2).ID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.router.Delegate(context.Background(), baseDelegation(child, testAgent(2).ID, testAgent(1).ID)); err == nil {
			t.Fatal("A to B to A cycle succeeded")
		}
	})
}

func TestParentParallelLimitBoundsOpenChildren(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	parent := f.registerParent(t, testAgent(1).ID, core.TaskLimits{MaxParallel: 1})
	if _, err := f.router.Delegate(context.Background(), baseDelegation(parent, testAgent(1).ID, testAgent(2).ID)); err != nil {
		t.Fatal(err)
	}
	request := baseDelegation(parent, testAgent(1).ID, testAgent(3).ID)
	if _, err := f.router.Delegate(context.Background(), request); err == nil || !strings.Contains(err.Error(), "parallel") {
		t.Fatalf("parallel limit error = %v", err)
	}
}

func TestDuplicateOpenDelegationIsRejected(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	parent := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
	request := baseDelegation(parent, testAgent(1).ID, testAgent(2).ID)
	request.Objective = "  inspect   the parser "
	if _, err := f.router.Delegate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Objective = "inspect the parser"
	if _, err := f.router.Delegate(context.Background(), request); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate request error = %v", err)
	}
}

func TestPerTaskMessageBudgetRejectsExcessMessages(t *testing.T) {
	f := newRouterFixture(t, Limits{MaxMessagesPerTask: 1})
	parent := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
	first := basicMessage(testAgent(1).ID, testAgent(2).ID)
	first.TaskID = &parent.ID
	if _, err := f.router.Send(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := basicMessage(testAgent(1).ID, testAgent(2).ID)
	second.TaskID = &parent.ID
	if _, err := f.router.Send(context.Background(), second); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("budget error = %v", err)
	}
}

func TestConcurrentMessagesCannotExceedTaskBudget(t *testing.T) {
	f := newRouterFixture(t, Limits{MaxMessagesPerTask: 1})
	parent := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			message := basicMessage(testAgent(1).ID, testAgent(2).ID)
			message.TaskID = &parent.ID
			if _, err := f.router.Send(context.Background(), message); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 1 || len(f.router.Snapshot().Messages) != 1 {
		t.Fatalf("accepted %d messages with a one-message budget", accepted)
	}
}

func TestConsultationConcurrencyAndResultShape(t *testing.T) {
	f := newRouterFixture(t, Limits{MaxConcurrentConsultationsPerAgent: 1})
	request := ConsultationRequest{Requester: testAgent(1).ID, Target: testAgent(2).ID, Question: "Is this safe?", Evidence: []string{"trace-1"}}
	consultation, err := f.router.Consult(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.router.Consult(context.Background(), request); err == nil || !strings.Contains(err.Error(), "concurren") {
		t.Fatalf("concurrency limit error = %v", err)
	}
	result := ConsultationResult{Findings: "safe with one caveat", Uncertainty: "moderate", References: []string{"note-2"}}
	completed, err := f.router.ResolveConsultation(context.Background(), consultation.ID, testAgent(2).ID, result)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Result == nil || completed.Result.Question != request.Question || !reflect.DeepEqual(completed.Result.Evidence, request.Evidence) || completed.Result.Findings != result.Findings {
		t.Fatalf("consultation result = %#v", completed.Result)
	}
	if len(f.delivery.snapshot()) != 2 {
		t.Fatalf("request and result deliveries = %d", len(f.delivery.snapshot()))
	}
}

func TestUnavailableRecipientsStayPendingThenDeliverOrFail(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		f := newRouterFixture(t, Limits{})
		f.directory.setAvailability(testAgent(2).ID, core.AvailabilityOffline)
		message, err := f.router.Send(context.Background(), basicMessage(testAgent(1).ID, testAgent(2).ID))
		if err != nil {
			t.Fatal(err)
		}
		if message.RecipientDelivery[testAgent(2).ID] != core.DeliveryPending || len(f.delivery.snapshot()) != 0 {
			t.Fatalf("offline message = %#v", message)
		}
		f.directory.setAvailability(testAgent(2).ID, core.AvailabilityAvailable)
		f.router.AgentAvailable(testAgent(2).ID)
		f.router.AgentAvailable(testAgent(2).ID)
		if got := len(f.delivery.snapshot()); got != 1 {
			t.Fatalf("delivery attempts = %d, want one", got)
		}
		if got := f.router.Snapshot().Messages[0].RecipientDelivery[testAgent(2).ID]; got != core.DeliveryDelivered {
			t.Fatalf("delivery state = %s", got)
		}
	})
	t.Run("pending age", func(t *testing.T) {
		f := newRouterFixture(t, Limits{MaxPendingAge: time.Minute})
		f.directory.setAvailability(testAgent(2).ID, core.AvailabilityOffline)
		_, err := f.router.Send(context.Background(), basicMessage(testAgent(1).ID, testAgent(2).ID))
		if err != nil {
			t.Fatal(err)
		}
		now := f.clock.Now().Add(time.Minute)
		f.clock.Set(now)
		f.router.Tick(now)
		message := f.router.Snapshot().Messages[0]
		if message.RecipientDelivery[testAgent(2).ID] != core.DeliveryFailed || message.DeliveryReason[testAgent(2).ID] == "" {
			t.Fatalf("expired pending message = %#v", message)
		}
	})
}

func TestDeliveryErrorFailsRecipientWithReason(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	f.delivery.errs[testAgent(2).ID] = errors.New("transport broke")
	message, err := f.router.Send(context.Background(), basicMessage(testAgent(1).ID, testAgent(2).ID))
	if err != nil {
		t.Fatal(err)
	}
	if message.RecipientDelivery[testAgent(2).ID] != core.DeliveryFailed || !strings.Contains(message.DeliveryReason[testAgent(2).ID], "transport broke") {
		t.Fatalf("delivery error was not stored: %#v", message)
	}
	if !containsAll(eventTypes(f.router.Snapshot().Events), "message.failed") {
		t.Fatal("missing message.failed event")
	}
}

func TestFailedDelegationDeliveryFailsChildAndNotifiesRequester(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	f.delivery.errs[testAgent(2).ID] = errors.New("transport broke")
	parent := f.registerParent(t, testAgent(1).ID, core.TaskLimits{})
	child, err := f.router.Delegate(context.Background(), baseDelegation(parent, testAgent(1).ID, testAgent(2).ID))
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != core.TaskFailed || !strings.Contains(child.FailureReason, "delivery error") {
		t.Fatalf("failed delegation child = %#v", child)
	}
	if got := len(f.router.Snapshot().Messages); got != 2 {
		t.Fatalf("messages = %d, want delegation and failure result", got)
	}
}

func TestApprovalGateAndPermissionIntersectionPreventEscalation(t *testing.T) {
	f := newRouterFixture(t, Limits{})
	a, b := testAgent(1), testAgent(2)
	a.PermissionPolicy = core.PermissionPolicy{Mode: core.PermissionModeAllowList, AllowTools: []string{"read"}}
	b.PermissionPolicy = core.PermissionPolicy{Mode: core.PermissionModeAllowList, AllowTools: []string{"read", "write"}}
	f.directory.agents[a.ID], f.directory.agents[b.ID] = a, b
	parent := f.registerParent(t, a.ID, core.TaskLimits{})
	request := baseDelegation(parent, a.ID, b.ID)
	request.RequiresApproval = true
	if _, err := f.router.Delegate(context.Background(), request); err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("approval-required delegation error = %v", err)
	}
	request.RequiresApproval = false
	task, err := f.router.Delegate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if task.PermissionPolicy == nil || !reflect.DeepEqual(task.PermissionPolicy.AllowTools, []string{"read"}) {
		t.Fatalf("permission escalation: %#v", task.PermissionPolicy)
	}
	request.PermissionPolicy = &core.PermissionPolicy{Mode: core.PermissionModeAsk}
	if _, err := f.router.Delegate(context.Background(), request); err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("ask mode error = %v", err)
	}
}

func eventTypes(events []core.Event) []string {
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, event.Type)
	}
	return got
}

func containsAll(got []string, want ...string) bool {
	set := map[string]bool{}
	for _, item := range got {
		set[item] = true
	}
	for _, item := range want {
		if !set[item] {
			return false
		}
	}
	return true
}

func cloneTestMessage(message core.Message) core.Message {
	message.Recipients = append([]core.AgentID(nil), message.Recipients...)
	return message
}
