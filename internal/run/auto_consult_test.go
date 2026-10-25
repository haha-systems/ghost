package run

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func TestAutoConsultDisabledDoesNotRequestDecision(t *testing.T) {
	service, agent := consultationDecisionService(t, "consult:shade")
	c, sessions := autoConsultController(t, false, service)
	startFailedDelegatedChild(t, c, sessions)
	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Tasks) == 2 && workspace.Tasks[1].Status == core.TaskFailed && workspace.Tasks[0].Status.IsTerminal()
	})
	if len(agent.Requests()) != 0 || len(c.Events(EventFilter{Type: "decision.requested"})) != 0 {
		t.Fatal("disabled auto consultation requested a decision")
	}
}

func TestAutoConsultEnabledConsultsOneCandidateOnChildFailure(t *testing.T) {
	service, agent := consultationDecisionService(t, "consult:shade")
	c, sessions := autoConsultController(t, true, service)
	startFailedDelegatedChild(t, c, sessions)
	// The router publishes the consultation before the owner records the
	// separate decision.applied execution record, so wait for both.
	waitFor(t, func() bool {
		return len(c.Events(EventFilter{Type: "coordination.consultation_requested"})) == 1 &&
			len(c.Events(EventFilter{Type: "decision.applied"})) == 1
	})
	requests := agent.Requests()
	if len(requests) != 1 || requests[0].Kind != core.DecisionKindConsultation || requests[0].TaskID != c.WorkspaceSnapshot().Tasks[0].ID {
		t.Fatalf("consultation decision requests = %#v", requests)
	}
	var state struct {
		FailedTaskID string `json:"failed_task_id"`
		Goal         string `json:"goal"`
		Failure      string `json:"failure_reason"`
		ParentAgent  string `json:"parent_agent"`
		Candidates   []struct {
			ID   core.AgentID `json:"id"`
			Name string       `json:"name"`
		} `json:"candidate_agents"`
	}
	if err := json.Unmarshal(requests[0].State, &state); err != nil || state.FailedTaskID == "" || state.Goal != "Review a small area" || state.Failure == "" || state.ParentAgent == "" || len(state.Candidates) != 2 {
		t.Fatalf("consultation decision state = %s, err=%v", requests[0].State, err)
	}
	if len(requests[0].Actions) != 3 || requests[0].Actions[0] != "none" || requests[0].Actions[1] != "consult:wraith" || requests[0].Actions[2] != "consult:shade" || state.Candidates[0].Name != "wraith" || state.Candidates[1].Name != "shade" {
		t.Fatalf("consultation candidates or actions = %#v / %#v", state.Candidates, requests[0].Actions)
	}
	consultations := c.Events(EventFilter{Type: "coordination.consultation_requested"})
	var consultation struct {
		Consultation struct {
			Requester core.AgentID `json:"requester"`
			Target    core.AgentID `json:"target"`
			Question  string       `json:"question"`
		} `json:"consultation"`
	}
	var shade core.AgentID
	for _, rosterAgent := range c.WorkspaceSnapshot().Agents {
		if rosterAgent.DisplayName == "shade" {
			shade = rosterAgent.ID
		}
	}
	if err := json.Unmarshal(consultations[0].Payload, &consultation); err != nil || consultation.Consultation.Target != shade || !strings.Contains(consultation.Consultation.Question, "Review a small area") {
		t.Fatalf("consultation event = %s, err=%v", consultations[0].Payload, err)
	}
	if got := c.Events(EventFilter{Type: "decision.applied"}); len(got) != 1 || got[0].CorrelationID != requests[0].ID {
		t.Fatalf("decision application records = %#v", got)
	}
}

func TestAutoConsultCanBeEnabledAtRuntimeWithRulesAgent(t *testing.T) {
	c, sessions := autoConsultController(t, false, nil)
	if err := c.SetAutoConsult(true); err != nil {
		t.Fatal(err)
	}
	startFailedDelegatedChild(t, c, sessions)
	waitFor(t, func() bool { return len(c.Events(EventFilter{Type: "coordination.consultation_requested"})) == 1 })
	var consultation struct {
		Consultation struct {
			Target core.AgentID `json:"target"`
		} `json:"consultation"`
	}
	events := c.Events(EventFilter{Type: "coordination.consultation_requested"})
	if err := json.Unmarshal(events[0].Payload, &consultation); err != nil {
		t.Fatal(err)
	}
	var shade core.AgentID
	for _, rosterAgent := range c.WorkspaceSnapshot().Agents {
		if rosterAgent.DisplayName == "shade" {
			shade = rosterAgent.ID
		}
	}
	if consultation.Consultation.Target != shade || !c.Status().DecisionAvailable {
		t.Fatalf("rules consultation target=%q shade=%q status=%#v", consultation.Consultation.Target, shade, c.Status())
	}
}

func TestAutoConsultDecisionBudgetExhaustedSkipsConsultation(t *testing.T) {
	service, agent := consultationDecisionService(t, "consult:shade")
	service.SetLimits(1, 1)
	c, sessions := autoConsultController(t, true, service)
	parent := c.WorkspaceSnapshot().Agents[0]
	if got := service.Decide(context.Background(), core.DecisionRequest{
		ID: "event_00000000000000010000000000000010", Kind: core.DecisionKindConsultation,
		TaskID: "task_00000000000000010000000000000010", RequestingAgent: parent.ID,
		Actions: []string{"none", "consult:shade"},
	}); got.Status != decision.StatusRecommended {
		t.Fatalf("budget priming decision = %#v", got)
	}
	startFailedDelegatedChild(t, c, sessions)
	waitFor(t, func() bool { return len(c.Events(EventFilter{Type: "decision.budget_exhausted"})) == 1 })
	if len(c.Events(EventFilter{Type: "coordination.consultation_requested"})) != 0 || len(agent.Requests()) != 1 {
		t.Fatalf("budget exhaustion consulted or called agent: consultations=%d agent calls=%d", len(c.Events(EventFilter{Type: "coordination.consultation_requested"})), len(agent.Requests()))
	}
}

func TestAutoConsultUnavailableAgentDoesNotFailRun(t *testing.T) {
	service := decision.NewService(decision.ServiceConfig{Enabled: map[core.DecisionKind]bool{core.DecisionKindConsultation: true}})
	c, sessions := autoConsultController(t, true, service)
	startFailedDelegatedChild(t, c, sessions)
	waitFor(t, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(c.Events(EventFilter{Type: "decision.unavailable"})) == 1 && len(workspace.Tasks) == 2 && workspace.Tasks[0].Status.IsTerminal()
	})
	if len(c.Events(EventFilter{Type: "coordination.consultation_requested"})) != 0 || c.Status().CoreError != nil {
		t.Fatalf("unavailable consultation changed run: consultations=%d core error=%v", len(c.Events(EventFilter{Type: "coordination.consultation_requested"})), c.Status().CoreError)
	}
}

func TestDelegationCanBeStoppedAndResumed(t *testing.T) {
	c, sessions := autoConsultController(t, false, nil)
	watchBrokerSessions(t, c, sessions)
	var parentTurns atomic.Int32
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		parentTurns.Add(1)
		if strings.Contains(input.Text, "Ghost rejected that communication request") {
			return channelEvent("wraith", "I will continue without delegation.")
		}
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + parent + `","objective":"Review a small area","context_refs":[],"expected_deliverable":"One finding"}` + "\n```"
		return channelEvent("wraith", block)
	}
	sessions["veil"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		child := frameValue(input.Text, "task_id")
		block := "```ghost-result\n" + `{"schema_version":1,"task_id":"` + child + `","status":"completed","result":"Review complete"}` + "\n```"
		return channelEvent("veil", block)
	}
	if err := c.SetDelegationEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := c.Send([]string{"wraith"}, "first review"); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		return len(c.Events(EventFilter{Type: "message.rejected"})) == 1 && c.WorkspaceSnapshot().Tasks[0].Status.IsTerminal()
	})
	if len(c.WorkspaceSnapshot().Tasks) != 1 || !strings.Contains(string(c.Events(EventFilter{Type: "message.rejected"})[0].Payload), "new agent delegations are disabled") {
		t.Fatal("stopped delegation created a child or did not record its reason")
	}
	waitFor(t, func() bool { return parentTurns.Load() == 2 && sessions["wraith"].State() == runtime.StateIdle })
	if err := c.SetDelegationEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := c.Send([]string{"wraith"}, "second review"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return len(c.WorkspaceSnapshot().Tasks) == 3 && c.WorkspaceSnapshot().Tasks[2].Status == core.TaskCompleted
	})
}

func consultationDecisionService(t *testing.T, action string) (*decision.Service, *decision.FakeAgent) {
	t.Helper()
	agent := decision.NewFakeAgent(decision.Info{ID: "consult-fake", Kinds: []core.DecisionKind{core.DecisionKindConsultation}}, decision.FakeStep{
		Response: core.DecisionResponse{Action: action, Uncertainty: .2},
	})
	service := decision.NewService(decision.ServiceConfig{Enabled: map[core.DecisionKind]bool{core.DecisionKindConsultation: true}})
	if err := service.Register(agent); err != nil {
		t.Fatal(err)
	}
	return service, agent
}

func autoConsultController(t *testing.T, enabled bool, service *decision.Service) (*Controller, map[string]*brokerTestSession) {
	t.Helper()
	agents := config.AgentSet{"wraith": {Runtime: "fake"}, "veil": {Runtime: "fake"}, "shade": {Runtime: "fake"}}
	cfg := config.Config{Agents: &agents, AgentOrder: []string{"wraith", "veil", "shade"}}
	cfg.Decisions.AutoConsult = enabled
	c := New(cfg, nil, Options{Runtime: fake.NewRuntime(), Decisions: service})
	sessions := map[string]*brokerTestSession{}
	installed := map[string]runtime.Session{}
	for _, name := range cfg.AgentOrder {
		session := &brokerTestSession{Session: fake.NewSession(name)}
		sessions[name] = session
		installed[name] = session
	}
	if err := c.SetSessions(installed); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
		for _, session := range sessions {
			_ = session.Close()
		}
	})
	return c, sessions
}

func startFailedDelegatedChild(t *testing.T, c *Controller, sessions map[string]*brokerTestSession) {
	t.Helper()
	watchBrokerSessions(t, c, sessions)
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + parent + `","objective":"Review a small area","context_refs":[],"expected_deliverable":"One finding"}` + "\n```"
		return channelEvent("wraith", block)
	}
	sessions["veil"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		child := frameValue(input.Text, "task_id")
		block := "```ghost-result\n" + `{"schema_version":1,"task_id":"` + child + `","status":"failed","result":"The review could not finish"}` + "\n```"
		return channelEvent("veil", block)
	}
	if err := c.Send([]string{"wraith"}, "Review a small area"); err != nil {
		t.Fatal(err)
	}
}
