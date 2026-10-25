package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
)

func (c *Controller) StartSessions() (map[string]runtime.Session, map[string]error) {
	sessions := map[string]runtime.Session{}
	failures := map[string]error{}
	_ = c.call(func(o *owner) {
		if o.stopping || o.phaseRuntime == nil || o.config.Agents == nil {
			return
		}
		ctx, cancel := context.WithTimeout(o.ctx, 15*time.Second)
		defer cancel()
		for id, backend := range *o.config.Agents {
			if backend.Runtime != "codex" {
				failures[id] = fmt.Errorf("unsupported runtime %q", backend.Runtime)
				continue
			}
			cfg, err := MakeSessionConfig(o.config.Global.InitialPrompt, id, backend)
			if err == nil {
				agentID, ok := o.workspace.agentID(id)
				if ok {
					cfg.Instructions = strings.TrimSpace(cfg.Instructions + "\n\n" + capabilityBrief(o.workspace, agentID))
				}
			}
			if err == nil {
				sessions[id], err = o.phaseRuntime.Start(ctx, cfg)
			}
			if err != nil {
				delete(sessions, id)
				failures[id] = err
			}
		}
		o.sessions = make(map[string]runtime.Session, len(sessions))
		for id, session := range sessions {
			o.sessions[id] = session
			if agentID, ok := o.workspace.agentID(id); ok {
				o.delivery.markBriefed(agentID)
				o.publishCapabilityBrief(agentID)
			}
		}
		o.sessionTransport.set(o.sessions)
		o.updateWorkspaceSessions(o.sessions)
	})
	return sessions, failures
}

// SetSessions installs persistent availability handles on the owner loop.
func (c *Controller) SetSessions(sessions map[string]runtime.Session) error {
	return c.SetSessionsWithFailures(sessions, nil)
}

// SetSessionsWithFailures installs session handles and reports permanent
// startup failures to pending router deliveries.
func (c *Controller) SetSessionsWithFailures(sessions map[string]runtime.Session, failures map[string]error) error {
	copied := map[string]runtime.Session{}
	for id, s := range sessions {
		copied[id] = s
	}
	failureCopy := map[string]string{}
	for name, err := range failures {
		if err != nil {
			failureCopy[name] = err.Error()
		}
	}
	var brokerDone <-chan error
	callErr := c.call(func(o *owner) {
		o.sessions = copied
		o.sessionTransport.set(copied)
		o.updateWorkspaceSessions(copied)
		for name, reason := range failureCopy {
			o.workspace.setAgentState(name, core.AvailabilityError, reason)
		}
		var available []core.AgentID
		for _, name := range o.workspace.agentNamesSnapshot() {
			id, ok := o.workspace.agentID(name)
			if !ok {
				continue
			}
			if session := copied[name]; session != nil {
				if session.State() == runtime.StateFailed || session.State() == runtime.StateStopped {
					if failureCopy[name] == "" {
						failureCopy[name] = "runtime session is offline"
					}
				} else {
					available = append(available, id)
				}
				if !o.capabilityEvents[id] {
					o.publishCapabilityBrief(id)
				}
			} else if failureCopy[name] == "" {
				continue // Startup is not complete; keep queued messages pending.
			}
		}
		if o.router != nil && (len(available) > 0 || len(failureCopy) > 0) {
			brokerDone = o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
				var failures []error
				for name, reason := range failureCopy {
					if id, ok := o.workspace.agentID(name); ok {
						failures = append(failures, router.AgentUnavailable(o.ctx, id, reason))
					}
				}
				for _, id := range available {
					router.AgentAvailable(id)
				}
				return errors.Join(failures...)
			}})
		}
	})
	if brokerDone != nil {
		return errors.Join(callErr, <-brokerDone)
	}
	return callErr
}

func (o *owner) sendPersistent(agent string, s runtime.Session, text string) {
	taskID := o.activeTaskID
	o.workers.Add(1)
	go func() {
		defer o.workers.Done()
		input := runtime.Input{Text: text}
		var err error
		if s.State() == runtime.StateRunning {
			err = s.Steer(o.ctx, input)
		} else {
			err = s.Send(o.ctx, input)
		}
		if err != nil {
			_ = o.controller.call(func(current *owner) {
				if current.activeTaskID == taskID {
					current.transitionProjectedTask(core.TaskFailed, err.Error())
				}
				current.record(agent, event.Event{Source: strings.ToUpper(agent), Kind: event.KindError, Message: err.Error()}, nil)
				current.publishBoundary()
			})
		}
	}()
}

func (o *owner) interruptPersistent(agent string, s runtime.Session) {
	o.workers.Add(1)
	go func() {
		defer o.workers.Done()
		if err := s.Interrupt(o.ctx); err != nil {
			_ = o.controller.Record(agent, event.Event{Source: strings.ToUpper(agent), Kind: event.KindError, Message: err.Error()}, nil)
		}
	}()
}

// Observe handles persistent-session QAC capture on the same owner loop as CES.
func (c *Controller) Observe(e runtime.Event) error {
	e = clone(e)
	return c.call(func(o *owner) {
		if o.stopping {
			return
		}
		o.projectObservedRuntime(e)
		if o.cognition == nil {
			return
		}
		plan, err := o.cognition.Observe(context.Background(), e, o.sessions, timeNow())
		if err != nil {
			o.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
		} else if plan != nil && o.cesActive() {
			o.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: "ignored qac request outside the CES phase path"})
		} else if plan != nil {
			o.emit(Event{AgentID: e.AgentID, ReplaceResponse: plan.Visible})
			o.dispatchRosterQAC(*plan)
		}
	})
}

func (o *owner) observePhase(e cognition.PhaseEvent) {
	if e.RunID == "" || e.RunID != o.cesRunID {
		return
	}
	if observer := phaseObserver(o.trace); observer != nil {
		observer(e)
	}
	if err := o.trace.Err(); err != nil {
		o.failTrace(err)
	}
	coreEvent := o.projectPhase(e)
	o.emit(Event{Phase: &e, CoreEvent: coreEvent})
	if e.Runtime != nil {
		o.projectObservedRuntime(*e.Runtime)
	}
}
