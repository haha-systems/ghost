package run

import (
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/history"
	"github.com/haha-systems/ghost/internal/runtime"
)

type cognitionResultMsg struct {
	plan cognition.Plan
	err  error
}

// qacMeta names the agents a cognition decision concerns. QAC plans address
// resources; history is keyed on agents, so the bindings are resolved here.
func (m *owner) qacMeta(plan cognition.Plan) map[string]string {
	meta := map[string]string{}
	if plan.WorkID != "" {
		meta[history.MetaWork] = plan.WorkID
	}
	if m.cognition == nil {
		return meta
	}
	if agent, ok := m.cognition.Agent(plan.From); ok {
		meta[history.MetaFrom] = agent
	}
	if agent, ok := m.cognition.Agent(plan.To); ok {
		meta[history.MetaTo] = agent
	}
	return meta
}

func (m *owner) dispatchCognition(plan cognition.Plan) {
	if plan.Action == "stop" {
		m.handleCognitionResult(cognitionResultMsg{plan: plan})
		return
	}
	agent, ok := m.cognition.Agent(plan.To)
	if !ok {
		m.handleCognitionResult(cognitionResultMsg{plan: plan, err: fmt.Errorf("unknown qac destination %q", plan.To)})
		return
	}
	s := m.sessions[agent]
	if s == nil {
		m.handleCognitionResult(cognitionResultMsg{plan: plan, err: fmt.Errorf("qac destination %q is unavailable", plan.To)})
		return
	}
	if err := m.cognition.BeginDispatch(plan); err != nil {
		m.handleCognitionResult(cognitionResultMsg{plan: plan, err: err})
		return
	}
	text := plan.Goal
	if plan.Action == "continue" {
		text = "[QAC DECISION]\n\nRemain at the current cognitive tier.\n\nContinue investigating."
	} else if plan.Action == "escalate" || plan.Action == "release" {
		work := m.cognition.Work()
		if work != nil {
			text = cognition.BuildHandoff(*work, plan.Request, plan.Decision)
		}
	}
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		var err error
		if s.State() != runtime.StateIdle {
			err = fmt.Errorf("qac destination %q is unavailable", plan.To)
		} else {
			err = s.Send(m.ctx, runtime.Input{Text: text})
		}
		_ = m.controller.call(func(o *owner) { o.handleCognitionResult(cognitionResultMsg{plan: plan, err: err}); o.publishBoundary() })
	}()
}
func (m *owner) handleCognitionResult(msg cognitionResultMsg) {
	if m.stopping {
		return
	}
	if msg.err != nil {
		m.cognition.Fail(msg.plan)
		m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: msg.err.Error()})
		return
	}
	if err := m.cognition.Commit(msg.plan, time.Now()); err != nil {
		m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
	}
	if !msg.plan.Initial && msg.plan.Action == "stop" {
		if m.orchestrator != nil && m.cesStore != nil {
			if err := m.orchestrator.ResourceUnavailable(); err == nil {
				state := m.cesStore.State().Task
				m.cognition.SyncWorkStatus(cognition.WorkIncomplete, state.TerminalReason, time.Now())
				m.record("", event.Event{Source: "CES", Kind: event.KindCES, Message: "work incomplete: " + state.TerminalReason}, map[string]string{"semantic_kind": string(epistemic.SemanticWorkIncomplete)})
			}
		}
	}
	m.publishCES()
	if !msg.plan.Initial {
		m.record("", event.Event{Source: "QAC", Kind: event.KindQAC, Message: fmt.Sprintf("%s %s → %s  %.2f / %.2f", strings.ToUpper(string(msg.plan.Action)), strings.ToUpper(msg.plan.Decision.From), strings.ToUpper(msg.plan.Decision.To), msg.plan.Decision.Score, msg.plan.Decision.Threshold)}, m.qacMeta(msg.plan))
	}
	return
}
