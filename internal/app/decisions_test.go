package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/decision"
	"github.com/haha-systems/ghost/internal/run"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func TestDecisionInspectorGroupsRequestsAndShowsFullDetails(t *testing.T) {
	m, _ := conversationModel(t, "wraith")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	first := core.DecisionRequest{
		ID: "event_00000000000000010000000000000001", Kind: core.DecisionKindCESMode,
		TaskID: "task_00000000000000010000000000000001", RequestingAgent: m.projection.Agents[0].ID,
		State:       json.RawMessage(`{"goal":"inspect the event path","qac_configured":true}`),
		Actions:     []string{"ces_on", "ces_off"},
		Constraints: core.DecisionConstraints{ManualOverride: "operator choice takes precedence", Notes: "test inputs"},
		Budget:      core.DecisionBudget{Timeout: time.Second},
	}
	second := first
	second.ID = "event_00000000000000010000000000000002"
	second.Kind = core.DecisionKindConsultation
	second.Actions = []string{"none", "consult:wraith"}
	m.projection.Events = []core.Event{
		decisionTestEvent(first.ID, "decision.requested", first, time.Unix(1, 0)),
		decisionTestEvent(first.ID, "decision.recommended", core.DecisionResponse{
			RequestID: first.ID, Action: "ces_on", Uncertainty: .25,
			Supporting: map[string]string{"heuristic": "numbered list"}, ModelOutput: json.RawMessage(`{"choice":"ces_on"}`),
			Meta: core.DecisionMeta{AgentID: "rules", Version: "1", Model: "heuristic"},
		}, time.Unix(2, 0)),
		decisionTestEvent(first.ID, "decision.applied", map[string]any{"request_id": first.ID, "action": "ces_on", "source": "decision"}, time.Unix(3, 0)),
		decisionTestEvent(second.ID, "decision.requested", second, time.Unix(4, 0)),
	}
	m, _ = sendCommand(t, m, ":decisions")
	list := m.View().Content
	for _, want := range []string{"DECISIONS", "CES MODE", "CONSULTATION", string(first.ID), string(second.ID)} {
		if !strings.Contains(list, want) {
			t.Errorf("decision list is missing %q:\n%s", want, list)
		}
	}
	m, _ = updateModel(t, m, press("enter", tea.KeyEnter))
	requestDetail := m.View().Content
	for _, want := range []string{"Request inputs", "inspect the event path", "ces_on", "operator choice takes precedence"} {
		if !strings.Contains(requestDetail, want) {
			t.Errorf("decision inputs are missing %q:\n%s", want, requestDetail)
		}
	}
	m, _ = updateModel(t, m, press("pgdown", tea.KeyPgDown))
	detail := m.View().Content
	for _, want := range []string{"Decision outputs", "numbered list", "model_output", "heuristic"} {
		if !strings.Contains(detail, want) {
			t.Errorf("decision outputs are missing %q:\n%s", want, detail)
		}
	}
	m, _ = updateModel(t, m, press("pgdown", tea.KeyPgDown))
	for _, want := range []string{"Execution records", "decision.applied"} {
		if !strings.Contains(m.View().Content, want) {
			t.Errorf("decision execution record is missing %q:\n%s", want, m.View().Content)
		}
	}
}

func TestCESAutoCycleAndDecisionSupervisionCommands(t *testing.T) {
	m, _ := conversationModel(t, "wraith")
	for _, want := range []run.CESPolicy{run.CESPolicyOn, run.CESPolicyAuto, run.CESPolicyOff} {
		m, _ = updateModel(t, m, press("c", 'c'))
		if got := m.controller.Status().Requested; got != want {
			t.Fatalf("CES cycle requested %q, want %q", got, want)
		}
	}
	m, _ = sendCommand(t, m, ":auto-consult on")
	if !m.controller.Status().AutoConsult {
		t.Fatal(":auto-consult on did not enable consultation")
	}
	m, _ = sendCommand(t, m, ":auto-consult off")
	if m.controller.Status().AutoConsult {
		t.Fatal(":auto-consult off did not disable consultation")
	}
	m, _ = sendCommand(t, m, ":delegation off")
	if m.controller.Status().DelegationEnabled {
		t.Fatal(":delegation off did not stop new delegations")
	}
	m, _ = sendCommand(t, m, ":delegation on")
	if !m.controller.Status().DelegationEnabled {
		t.Fatal(":delegation on did not resume delegations")
	}
	m, _ = sendCommand(t, m, ":decision override event_00000000000000010000000000000001 ces_on")
	if !strings.Contains(m.conversation.notice, "not pending") {
		t.Fatalf("override error notice = %q", m.conversation.notice)
	}
}

func TestDecisionOverrideCommandAppliesPendingCESChoice(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	agent := decision.NewFakeAgent(decision.Info{ID: "fake", Kinds: []core.DecisionKind{core.DecisionKindCESMode}}, decision.FakeStep{
		Started: started, Release: release, Response: core.DecisionResponse{Action: "ces_on", Uncertainty: .1},
	})
	service := decision.NewService(decision.ServiceConfig{Enabled: map[core.DecisionKind]bool{core.DecisionKindCESMode: true}})
	if err := service.Register(agent); err != nil {
		t.Fatal(err)
	}
	m, _ := decisionConversationModel(t, appQACConfig(), service)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = sendCommand(t, m, ":ces auto")
	if err := m.controller.Start("operator will choose the CES mode"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("decision agent did not start")
	}
	requestID := m.controller.Status().CESAuto.RequestID
	m, _ = sendCommand(t, m, ":decision override "+string(requestID)+" ces_off")
	if m.conversation.notice != "decision overridden" {
		t.Fatalf("override notice = %q", m.conversation.notice)
	}
	if status := m.controller.Status(); status.CESAuto.Source != "override" || status.CESAuto.Executed != "ces_off" {
		t.Fatalf("override status = %#v", status.CESAuto)
	}
	if len(m.controller.Events(run.EventFilter{Type: "decision.overridden"})) != 1 {
		t.Fatal("TUI override did not record decision.overridden")
	}
	close(release)
	waitFor(t, func() bool {
		return len(m.controller.Events(run.EventFilter{Type: "decision.ignored"})) == 1
	})
	_ = m.controller.Cancel()
}

func TestDecisionCommandsAppearInHelpAndPalette(t *testing.T) {
	m, _ := conversationModel(t, "wraith")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = sendCommand(t, m, ":help")
	for _, want := range []string{":decisions", ":decision override", ":auto-consult", ":delegation", ":ces auto"} {
		if !strings.Contains(m.View().Content, want) {
			t.Errorf("help does not list %q:\n%s", want, m.View().Content)
		}
	}
	m, _ = sendCommand(t, m, ":help")
	m, _ = updateModel(t, m, control('p'))
	for _, want := range []string{":decisions", ":ces auto"} {
		if !strings.Contains(m.View().Content, want) {
			t.Errorf("command palette does not list %q", want)
		}
	}
	m.conversation.paletteFilter = "auto-consult"
	if !strings.Contains(m.View().Content, ":auto-consult on") {
		t.Error("command palette does not list auto-consult commands")
	}
	m.conversation.paletteFilter = "delegation"
	if !strings.Contains(m.View().Content, ":delegation off") {
		t.Error("command palette does not list delegation commands")
	}
}

func TestCESAutoHeaderShowsDecisionProgressAndAvailability(t *testing.T) {
	m, _ := conversationModel(t, "wraith")
	m.width, m.height = 80, 24
	m.runStatus = run.Status{
		Requested: run.CESPolicyAuto, DecisionAvailable: true,
		CESAuto: run.CESAutoStatus{Pending: true, RequestID: "event_00000000000000010000000000000001", Source: "decision"},
	}
	view := m.View().Content
	for _, want := range []string{"CES AUTO", "decision pending", "source decision", "decision service available"} {
		if !strings.Contains(view, want) {
			t.Errorf("pending auto status is missing %q:\n%s", want, view)
		}
	}
	m.runStatus.CESAuto = run.CESAutoStatus{Recommended: "ces_on", Executed: "ces_off", Source: "override"}
	view = m.View().Content
	for _, want := range []string{"recommended ces_on", "executed ces_off", "override"} {
		if !strings.Contains(view, want) {
			t.Errorf("completed auto status is missing %q:\n%s", want, view)
		}
	}
}

func decisionTestEvent(requestID core.EventID, eventType string, payload any, at time.Time) core.Event {
	encoded, _ := json.Marshal(payload)
	return core.Event{
		ID: core.EventID("event_00000000000000010000000000000009"), Time: at,
		Type: eventType, Category: core.CategoryDecision, CorrelationID: requestID,
		CausationID: requestID, Payload: encoded,
	}
}

func decisionConversationModel(t *testing.T, cfg config.Config, service *decision.Service) (Model, map[string]*fake.Session) {
	t.Helper()
	m := New(cfg, nil)
	_ = m.controller.Close()
	c := run.New(cfg, nil, run.Options{Runtime: fake.NewRuntime(), Decisions: service})
	sessions := map[string]*fake.Session{}
	installed := map[string]runtime.Session{}
	for name := range *cfg.Agents {
		session := fake.NewSession(name)
		sessions[name], installed[name] = session, session
	}
	if err := c.SetSessions(installed); err != nil {
		t.Fatal(err)
	}
	m.controller = c
	m.runEvents, _ = c.Subscribe()
	m.sessions = installed
	m.conversation = newConversationState()
	m.refreshProjection()
	t.Cleanup(func() {
		_ = c.Close()
		for _, session := range sessions {
			_ = session.Close()
		}
	})
	return m, sessions
}
