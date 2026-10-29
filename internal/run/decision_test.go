package run

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func TestOwnerDecisionRunsOffOwnerAndPersistsRecommendation(t *testing.T) {
	requestID := core.EventID("event_00000000000000010000000000000001")
	agent := decision.NewFakeAgent(decision.Info{ID: "fake", Kinds: []core.DecisionKind{core.DecisionKindCESMode}}, decision.FakeStep{
		Delay:    40 * time.Millisecond,
		Response: core.DecisionResponse{Action: "ces_off", Uncertainty: .1, ModelOutput: json.RawMessage(`{"choice":"ces_off"}`)},
	})
	service := decision.NewService(decision.ServiceConfig{Enabled: map[core.DecisionKind]bool{core.DecisionKindCESMode: true}})
	if err := service.Register(agent); err != nil {
		t.Fatal(err)
	}
	service.SetLimits(10, 1)
	workdir := t.TempDir()
	c := New(config.Default(), nil, Options{Runtime: fake.NewRuntime(), WorkDir: workdir, Decisions: service})
	t.Cleanup(func() { _ = c.Close() })
	req := core.DecisionRequest{
		ID: requestID, Kind: core.DecisionKindCESMode,
		TaskID: "task_00000000000000010000000000000001", RequestingAgent: "agent_00000000000000010000000000000001",
		State: json.RawMessage(`{"goal":"small task"}`), Actions: []string{"ces_on", "ces_off"},
	}
	started := time.Now()
	if err := c.call(func(o *owner) { o.requestDecision(req) }); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 30*time.Millisecond {
		t.Fatalf("owner call waited for agent: %s", elapsed)
	}
	if got := c.Status(); got.CoreError != nil {
		t.Fatalf("owner did not remain responsive: %v", got.CoreError)
	}
	var recommended core.Event
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events := c.Events(EventFilter{Type: "decision.recommended"})
		if len(events) > 0 {
			recommended = events[len(events)-1]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if recommended.ID == "" || recommended.CorrelationID != requestID || !strings.Contains(string(recommended.Payload), `"model_output":{"choice":"ces_off"}`) {
		t.Fatalf("recommendation event = %#v", recommended)
	}
	if len(c.Events(EventFilter{Type: "decision.requested"})) != 1 {
		t.Fatal("request event was not recorded")
	}
	budgetRequest := req
	budgetRequest.ID = "event_00000000000000010000000000000002"
	unavailableRequest := req
	unavailableRequest.ID = "event_00000000000000010000000000000003"
	unavailableRequest.Kind = core.DecisionKindConsultation
	if err := c.call(func(o *owner) {
		o.requestDecision(budgetRequest)
		o.requestDecision(unavailableRequest)
	}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.Events(EventFilter{Type: "decision.budget_exhausted"})) == 1 && len(c.Events(EventFilter{Type: "decision.unavailable"})) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(c.Events(EventFilter{Type: "decision.budget_exhausted"})) != 1 || len(c.Events(EventFilter{Type: "decision.unavailable"})) != 1 {
		t.Fatal("budget and unavailable outcomes were not recorded")
	}
	if err := c.call(func(o *owner) {
		o.recordDecisionApplied(requestID, "ces_off")
		o.recordDecisionOverridden(requestID, "ces_on", "operator choice")
		o.recordDecisionIgnored(requestID, "ces_off", "unsafe boundary")
	}); err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{"decision.applied", "decision.overridden", "decision.ignored"} {
		events := c.Events(EventFilter{Type: eventType})
		if len(events) != 1 || events[0].CorrelationID != requestID || events[0].CausationID != requestID {
			t.Errorf("%s events = %#v", eventType, events)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := New(config.Default(), nil, Options{WorkDir: workdir})
	t.Cleanup(func() { _ = restarted.Close() })
	for eventType, want := range map[string]int{
		"decision.requested": 3, "decision.recommended": 1, "decision.unavailable": 1,
		"decision.budget_exhausted": 1, "decision.applied": 1, "decision.overridden": 1, "decision.ignored": 1,
	} {
		if got := restarted.Events(EventFilter{Type: eventType}); len(got) != want {
			t.Errorf("replay %s events = %d, want %d", eventType, len(got), want)
		}
	}
}

func TestControllerDecisionConfigIsOffByDefaultAndBuildsRulesWhenEnabled(t *testing.T) {
	request := core.DecisionRequest{
		ID: "event_00000000000000010000000000000011", Kind: core.DecisionKindCESMode,
		TaskID: "task_00000000000000010000000000000011", RequestingAgent: "agent_00000000000000010000000000000011",
		State: json.RawMessage(`{"goal":"Fix one label"}`), Actions: []string{"ces_on", "ces_off"},
	}
	disabled := New(config.Default(), nil, Options{Runtime: fake.NewRuntime()})
	if err := disabled.call(func(o *owner) { o.requestDecision(request) }); err != nil {
		t.Fatal(err)
	}
	if len(disabled.Events(EventFilter{Type: "decision.requested"})) != 0 {
		t.Fatal("disabled config recorded a decision")
	}
	if err := disabled.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Decisions.Enabled = true
	enabled := New(cfg, nil, Options{Runtime: fake.NewRuntime()})
	t.Cleanup(func() { _ = enabled.Close() })
	if err := enabled.call(func(o *owner) { o.requestDecision(request) }); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := enabled.Events(EventFilter{Type: "decision.recommended"}); len(got) == 1 {
			if !strings.Contains(string(got[0].Payload), `"action":"ces_off"`) {
				t.Fatalf("rules response = %s", got[0].Payload)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("enabled config did not produce a rules recommendation")
}
