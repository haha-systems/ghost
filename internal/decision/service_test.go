package decision_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
)

func request(id, task string) core.DecisionRequest {
	return core.DecisionRequest{ID: core.EventID(id), Kind: core.DecisionKindCESMode, TaskID: core.TaskID(task), RequestingAgent: "agent_00000000000000010000000000000001", Actions: []string{"ces_on", "ces_off"}}
}

func response(action string) core.DecisionResponse {
	return core.DecisionResponse{Action: action, Uncertainty: .2}
}

func newFake(t *testing.T, steps ...decision.FakeStep) (*decision.Service, *decision.FakeAgent) {
	t.Helper()
	agent := decision.NewFakeAgent(decision.Info{ID: "fake", Version: "1", Kinds: []core.DecisionKind{core.DecisionKindCESMode}}, steps...)
	service := decision.NewService(decision.ServiceConfig{Enabled: map[core.DecisionKind]bool{core.DecisionKindCESMode: true}})
	if err := service.Register(agent); err != nil {
		t.Fatal(err)
	}
	return service, agent
}

func TestServiceReturnsValidRecommendationAndRecordsRequest(t *testing.T) {
	service, agent := newFake(t, decision.FakeStep{Response: response("ces_on")})
	req := request("event_00000000000000010000000000000001", "task_00000000000000010000000000000001")
	outcome := service.Decide(context.Background(), req)
	if outcome.Status != decision.StatusRecommended || outcome.Response == nil || outcome.Response.Action != "ces_on" || outcome.Response.RequestID != req.ID {
		t.Fatalf("outcome = %#v", outcome)
	}
	if got := agent.Requests(); len(got) != 1 || got[0].ID != req.ID {
		t.Fatalf("requests = %#v", got)
	}
}

func TestServiceDisabledAndPerTaskPerRunBudgets(t *testing.T) {
	service, agent := newFake(t,
		decision.FakeStep{Response: response("ces_off")},
		decision.FakeStep{Response: response("ces_off")},
	)
	service.SetEnabled(core.DecisionKindCESMode, false)
	if got := service.Decide(context.Background(), request("event_00000000000000010000000000000001", "task_00000000000000010000000000000001")); got.Status != decision.StatusDisabled {
		t.Fatalf("disabled status = %q", got.Status)
	}
	service.SetEnabled(core.DecisionKindCESMode, true)
	service.SetLimits(1, 1)
	first := service.Decide(context.Background(), request("event_00000000000000010000000000000002", "task_00000000000000010000000000000001"))
	if first.Status != decision.StatusRecommended {
		t.Fatalf("first status = %q", first.Status)
	}
	if got := service.Decide(context.Background(), request("event_00000000000000010000000000000003", "task_00000000000000010000000000000001")); got.Status != decision.StatusBudgetExhausted {
		t.Fatalf("task budget status = %q", got.Status)
	}
	if got := service.Decide(context.Background(), request("event_00000000000000010000000000000004", "task_00000000000000000000000000000002")); got.Status != decision.StatusBudgetExhausted {
		t.Fatalf("run budget status = %q", got.Status)
	}
	if got := agent.Requests(); len(got) != 1 {
		t.Fatalf("agent calls = %d", len(got))
	}
}

func TestServiceTimeoutAndCircuitBreakerReset(t *testing.T) {
	service, _ := newFake(t,
		decision.FakeStep{Delay: 40 * time.Millisecond, Response: response("ces_on")},
		decision.FakeStep{Err: errors.New("failed")},
		decision.FakeStep{Err: errors.New("failed")},
		decision.FakeStep{Response: response("ces_off")},
	)
	service.SetTimeout(5 * time.Millisecond)
	service.SetFailureThreshold(3)
	service.SetLimits(10, 10)
	if got := service.Decide(context.Background(), request("event_00000000000000010000000000000001", "task_00000000000000010000000000000001")); got.Status != decision.StatusError {
		t.Fatalf("timeout status = %q", got.Status)
	}
	service.SetTimeout(time.Second)
	for i := 2; i <= 3; i++ {
		id := "event_0000000000000001000000000000000" + string(rune('0'+i))
		if got := service.Decide(context.Background(), request(id, "task_00000000000000010000000000000001")); got.Status != decision.StatusError {
			t.Fatalf("failure %d status = %q", i, got.Status)
		}
	}
	if got := service.Decide(context.Background(), request("event_00000000000000010000000000000004", "task_00000000000000010000000000000001")); got.Status != decision.StatusUnavailable {
		t.Fatalf("open breaker status = %q", got.Status)
	}
	service.Reset(core.DecisionKindCESMode)
	if got := service.Decide(context.Background(), request("event_00000000000000010000000000000005", "task_00000000000000010000000000000001")); got.Status != decision.StatusRecommended {
		t.Fatalf("post-reset status = %q (%s)", got.Status, got.Reason)
	}
}

func TestServiceMarksBadResponseInvalid(t *testing.T) {
	service, _ := newFake(t, decision.FakeStep{Response: response("not-offered")})
	if got := service.Decide(context.Background(), request("event_00000000000000010000000000000001", "task_00000000000000010000000000000001")); got.Status != decision.StatusInvalid {
		t.Fatalf("status = %q, want invalid", got.Status)
	}
}

type ignoresCancelAgent struct{ release <-chan struct{} }

func (ignoresCancelAgent) Info() decision.Info {
	return decision.Info{ID: "slow", Kinds: []core.DecisionKind{core.DecisionKindCESMode}}
}
func (a ignoresCancelAgent) Decide(_ context.Context, request core.DecisionRequest) (core.DecisionResponse, error) {
	<-a.release
	result := response("ces_off")
	result.RequestID = request.ID
	return result, nil
}

func TestServiceReturnsAtTimeoutWhenAgentIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	service := decision.NewService(decision.ServiceConfig{Timeout: 10 * time.Millisecond})
	if err := service.Register(ignoresCancelAgent{release: release}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	got := service.Decide(context.Background(), request("event_00000000000000010000000000000001", "task_00000000000000010000000000000001"))
	close(release)
	if got.Status != decision.StatusError || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("outcome = %#v, elapsed = %s", got, time.Since(started))
	}
}

type panicAgent struct{}

func (panicAgent) Info() decision.Info {
	return decision.Info{ID: "panic", Kinds: []core.DecisionKind{core.DecisionKindCESMode}}
}
func (panicAgent) Decide(context.Context, core.DecisionRequest) (core.DecisionResponse, error) {
	panic("agent panic")
}

func TestServiceConvertsAgentPanicToError(t *testing.T) {
	service := decision.NewService(decision.ServiceConfig{})
	if err := service.Register(panicAgent{}); err != nil {
		t.Fatal(err)
	}
	got := service.Decide(context.Background(), request("event_00000000000000010000000000000001", "task_00000000000000010000000000000001"))
	if got.Status != decision.StatusError {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestServiceConcurrentCallsAndZeroValue(t *testing.T) {
	steps := make([]decision.FakeStep, 6)
	for i := range steps {
		steps[i] = decision.FakeStep{Response: response("ces_off")}
	}
	agent := decision.NewFakeAgent(decision.Info{ID: "fake", Kinds: []core.DecisionKind{core.DecisionKindCESMode}}, steps...)
	var service decision.Service
	service.SetEnabled(core.DecisionKindCESMode, true)
	if err := service.Register(agent); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan string, len(steps))
	for i := range steps {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			id := fmt.Sprintf("%032x", index+1)
			got := service.Decide(context.Background(), request("event_"+id, "task_"+id))
			if got.Status != decision.StatusRecommended {
				errorsSeen <- fmt.Sprintf("call %d status %s", index, got.Status)
			}
		}(i)
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	if got := len(agent.Requests()); got != len(steps) {
		t.Fatalf("recorded requests = %d, want %d", got, len(steps))
	}
}
