package run

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
)

// cesWait holds a QAC plan while its agent's long-lived session is made
// ready (on_unavailable = "wait"). A busy session is waited on, and a stopped
// or failed one is restarted once. The plan is committed only when the
// session is idle, so a timeout or cancellation leaves QAC unchanged.
type cesWait struct {
	plan       cognition.Plan
	run        cesRun
	agent      string
	restarting bool
	restarted  bool
	stopTimer  func() bool
	cancel     context.CancelFunc
}

// SessionRestart carries a long-lived session that CES restarted, so the
// operator surface can follow its events.
type SessionRestart struct {
	AgentID string
	Session runtime.Session
}

func sessionState(s runtime.Session) runtime.SessionState {
	if s == nil {
		return runtime.StateStopped
	}
	return s.State()
}

func (m *owner) beginCESWait(w *cesWait) {
	timeout := m.config.QAC.UnavailableTimeout()
	m.cesWait = w
	runID := w.run.id
	w.stopTimer = m.afterFunc(timeout, func() {
		_ = m.controller.call(func(o *owner) { o.expireCESWait(runID, timeout); o.publishBoundary() })
	})
	state := sessionState(m.sessions[w.agent])
	m.traceCES(w.run, cognition.PhaseQACResourceWaiting, "state="+string(state), "", "", map[string]string{"resource": w.plan.To, "state": string(state), "timeout_ms": strconv.FormatInt(timeout.Milliseconds(), 10), "qac_plan_id": w.plan.ID})
	m.record("", event.Event{Source: "QAC", Kind: event.KindStatus, Message: fmt.Sprintf("waiting for %s (%s) before %s", strings.ToUpper(w.agent), state, strings.ToUpper(string(w.run.phase)))}, map[string]string{"resource": w.plan.To, "phase_run_id": w.run.id})
	m.setCESResourceStatus(epistemic.WorkActive, fmt.Sprintf("waiting for %s", w.agent))
	m.advanceCESWait()
}

// advanceCESWait runs on the owner loop after the wait starts, after each
// event from the agent's session, and after a restart.
func (m *owner) advanceCESWait() {
	w := m.cesWait
	if w == nil || w.restarting {
		return
	}
	switch state := sessionState(m.sessions[w.agent]); {
	case state == runtime.StateIdle:
		m.clearCESWait()
		m.traceCES(w.run, cognition.PhaseQACResourceReady, strings.ToUpper(w.agent), "", "", map[string]string{"resource": w.plan.To, "restarted": strconv.FormatBool(w.restarted)})
		m.record("", event.Event{Source: "QAC", Kind: event.KindStatus, Message: strings.ToUpper(w.agent) + " is ready"}, map[string]string{"resource": w.plan.To, "phase_run_id": w.run.id})
		controller := cesController{store: m.cesStore, coordinator: m.cognition}
		outcome := controller.commitPlan(w.plan, w.run, m)
		if outcome.terminal != nil {
			m.cesTerminal(outcome.terminal.status, outcome.terminal.reason, outcome.terminal.detail)
		} else if outcome.plan != nil {
			m.runPhase(*outcome.plan)
		}
	case state == runtime.StateStopped || state == runtime.StateFailed:
		if w.restarted {
			m.failCESWait(fmt.Sprintf("%s session is %s after a restart", strings.ToUpper(w.agent), state))
			return
		}
		m.restartCESSession(w, state)
	}
}

// observeCESWait advances the wait after an event from the agent's session
// and publishes a boundary once the wait has ended.
func (m *owner) observeCESWait() {
	m.advanceCESWait()
	if m.cesWait == nil {
		m.publishBoundary()
	}
}

// restartCESSession starts the agent's long-lived session again off the owner
// loop. The runtime replaces the old session for that agent.
func (m *owner) restartCESSession(w *cesWait, state runtime.SessionState) {
	cfg, err := m.rosterSessionConfig(w.agent)
	if err != nil || m.phaseRuntime == nil {
		m.failCESWait(fmt.Sprintf("restart %s: %v", strings.ToUpper(w.agent), err))
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	w.restarting, w.cancel = true, cancel
	m.traceCES(w.run, cognition.PhaseQACResourceRestart, "state="+string(state), "", "", map[string]string{"resource": w.plan.To, "state": string(state)})
	m.record("", event.Event{Source: "QAC", Kind: event.KindStatus, Message: fmt.Sprintf("restarting %s (%s)", strings.ToUpper(w.agent), state)}, map[string]string{"resource": w.plan.To, "phase_run_id": w.run.id})
	rt, agent, runID := m.phaseRuntime, w.agent, w.run.id
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer cancel()
		s, err := rt.Start(ctx, cfg)
		_ = m.controller.call(func(o *owner) { o.finishCESRestart(runID, agent, s, err); o.publishBoundary() })
	}()
}

func (m *owner) finishCESRestart(runID, agent string, s runtime.Session, err error) {
	if m.stopping {
		return
	}
	if err == nil {
		// The new session replaced the old one in the runtime, so install it
		// even if the wait has since ended.
		m.sessions = maps.Clone(m.sessions)
		m.sessions[agent] = s
		m.sessionTransport.set(m.sessions)
		m.updateWorkspaceSessions(m.sessions)
		m.emit(Event{AgentID: agent, RestartedSession: &SessionRestart{AgentID: agent, Session: s}})
	}
	w := m.cesWait
	if w == nil || w.run.id != runID {
		return
	}
	w.restarting, w.restarted, w.cancel = false, true, nil
	if err != nil {
		m.failCESWait(fmt.Sprintf("restart %s: %v", strings.ToUpper(agent), err))
		return
	}
	m.advanceCESWait()
}

func (m *owner) expireCESWait(runID string, timeout time.Duration) {
	if w := m.cesWait; w != nil && w.run.id == runID && !m.stopping {
		m.failCESWait(fmt.Sprintf("%s was not ready within %s (%s)", strings.ToUpper(w.agent), timeout, sessionState(m.sessions[w.agent])))
	}
}

func (m *owner) failCESWait(detail string) {
	w := m.cesWait
	m.clearCESWait()
	m.traceCES(w.run, cognition.PhaseQACAllocFailed, "resource not ready", "", "", map[string]string{"requested_phase": string(w.run.phase), "resource": w.plan.To, "error": detail})
	m.cesTerminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", detail)
}

// clearCESWait stops the timer and any restart. Terminal work clears it too.
func (m *owner) clearCESWait() {
	w := m.cesWait
	if w == nil {
		return
	}
	m.cesWait = nil
	if w.stopTimer != nil {
		w.stopTimer()
	}
	if w.cancel != nil {
		w.cancel()
	}
}

func defaultAfterFunc(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
