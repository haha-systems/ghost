package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
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
		}
	})
	return sessions, failures
}

// SetSessions installs persistent availability handles on the owner loop.
func (c *Controller) SetSessions(sessions map[string]runtime.Session) error {
	copied := map[string]runtime.Session{}
	for id, s := range sessions {
		copied[id] = s
	}
	return c.call(func(o *owner) { o.sessions = copied })
}

func (o *owner) sendPersistent(agent string, s runtime.Session, text string) {
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
			_ = o.controller.Record(agent, event.Event{Source: strings.ToUpper(agent), Kind: event.KindError, Message: err.Error()}, nil)
		}
	}()
}

// Observe handles persistent-session QAC capture on the same owner loop as CES.
func (c *Controller) Observe(e runtime.Event) error {
	e = clone(e)
	return c.call(func(o *owner) {
		if o.stopping || o.cognition == nil {
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
	o.emit(Event{Phase: &e})
}
