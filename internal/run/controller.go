// Package run owns run execution without a terminal interface.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/haha-systems/ghost/internal/codex"
	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/runtime"
	ghosttrace "github.com/haha-systems/ghost/internal/trace"
)

var ErrClosed = errors.New("run controller closed")

type CESPolicy string

const (
	CESPolicyOn  CESPolicy = "on"
	CESPolicyOff CESPolicy = "off"
)

// Options injects the runtime and trace writer and selects an optional event
// log root. The controller owns the injected handles.
type Options struct {
	Runtime        runtime.Runtime
	Memory         MemoryService
	MemoryFactory  MemoryFactory
	Trace          *ghosttrace.Writer
	Progress       cognition.ProgressVerifier
	WorkDir        string
	RecoveryLimits RecoveryLimits
}

// Status is a detached snapshot. It contains no mutable controller handles.
type Status struct {
	Enabled       bool
	Active        bool
	Requested     CESPolicy
	Effective     CESPolicy
	Pending       bool
	PolicyReason  string
	State         epistemic.State
	View          epistemic.OperatorView
	Work          *cognition.WorkItem
	Resource      string
	PhaseRunID    string
	TraceError    error
	CoreError     error
	EventLogPath  string
	EventLogError error
	Recovery      RecoverySummary
}

// Event carries an ordered update to a client. Boundary marks a completed
// control operation or phase result. Semantic events retain their source data
// so the client can render them with its own display labels.
type Event struct {
	Entry           event.Event
	CoreEvent       *core.Event
	AgentID         string
	Phase           *cognition.PhaseEvent
	Semantic        *epistemic.SemanticEvent
	Status          *Status
	ResourceStatus  epistemic.WorkStatus
	Activity        string
	ReplaceResponse string
	Boundary        bool
}

type request struct {
	apply func(*owner)
	done  chan struct{}
}
type subscription struct {
	out   chan Event
	queue []Event
}

type Controller struct {
	requests  chan request
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

type owner struct {
	controller         *Controller
	router             *comms.Router
	brokerQueue        chan brokerCommand
	brokerEventCount   int
	memory             MemoryService
	memoryEvents       chan core.Event
	memorySinkDropped  atomic.Int64
	memoryCaptured     map[core.TaskID]bool
	pendingMemory      map[core.AgentID][]memoryDelivery
	sessionTransport   *sessionTransport
	delivery           *sessionDelivery
	config             config.Config
	logger             *slog.Logger
	phaseRuntime       runtime.Runtime
	trace              *ghosttrace.Writer
	traceError         error
	eventLog           *core.EventLog
	eventLogPath       string
	eventLogError      error
	ids                *core.IDGenerator
	recovery           RecoverySummary
	cognition          *cognition.Coordinator
	sessions           map[string]runtime.Session
	cesStore           *epistemic.Store
	orchestrator       *cognition.Orchestrator
	cesResource        string
	cesRunID           string
	cesPhase           epistemic.Phase
	cesRepeats         int
	cesPublished       int
	progressVerifier   cognition.ProgressVerifier
	cesRequested       CESPolicy
	cesEffective       CESPolicy
	cesPending         bool
	cesPolicyReason    string
	workspace          *workspaceProjection
	coreSequencer      *core.Sequencer
	projectionError    error
	activeTaskID       core.TaskID
	taskCorrelation    map[core.TaskID]core.EventID
	startRecipients    []core.AgentID
	startBroadcast     bool
	lastHumanMessage   core.MessageID
	replyBuffers       map[string]string
	taskPendingAgents  map[core.TaskID]map[string]bool
	agentTaskIDs       map[core.AgentID]core.TaskID
	pendingChannelWork map[core.TaskID]int
	rejectedNotices    map[core.TaskID]map[core.AgentID]bool
	interruptedTasks   map[core.TaskID]bool
	capabilityEvents   map[core.AgentID]bool
	cancel             context.CancelFunc
	workers            sync.WaitGroup
	subscribers        []*subscription
	approvals          map[string]*approvalRecord
	approvalSeq        int
	closing            bool
	stopping           bool
	ctx                context.Context
	stop               context.CancelFunc
}

func New(cfg config.Config, logger *slog.Logger, options Options) *Controller {
	c := &Controller{requests: make(chan request), done: make(chan struct{})}
	o := &owner{controller: c, config: cfg, logger: logger, phaseRuntime: options.Runtime, trace: options.Trace, progressVerifier: options.Progress, sessions: map[string]runtime.Session{}, coreSequencer: core.NewSequencer(), taskCorrelation: map[core.TaskID]core.EventID{}, replyBuffers: map[string]string{}, taskPendingAgents: map[core.TaskID]map[string]bool{}, agentTaskIDs: map[core.AgentID]core.TaskID{}, pendingChannelWork: map[core.TaskID]int{}, rejectedNotices: map[core.TaskID]map[core.AgentID]bool{}, interruptedTasks: map[core.TaskID]bool{}, capabilityEvents: map[core.AgentID]bool{}, memoryEvents: make(chan core.Event, 256), memoryCaptured: map[core.TaskID]bool{}, pendingMemory: map[core.AgentID][]memoryDelivery{}, ids: core.NewIDGenerator(time.Now, nil)}
	o.ctx, o.stop = context.WithCancel(context.Background())
	// Copy configuration maps before they are used by the owner.
	o.config = cloneConfig(cfg)
	history := recoveryHistory{}
	if options.WorkDir != "" {
		var historyErr error
		history, historyErr = readWorkspaceEventHistory(options.WorkDir, options.RecoveryLimits)
		if historyErr != nil {
			o.eventLogError = historyErr
			history.Warnings = append(history.Warnings, "event history could not be read: "+historyErr.Error())
			if logger != nil {
				logger.Error("read core event history", "error", historyErr)
			}
		}
	}
	for _, id := range history.ReservedEventIDs {
		_ = o.ids.Reserve(string(id))
	}
	workspaceID := history.WorkspaceID
	var idErr error
	if workspaceID == "" {
		var raw string
		raw, idErr = o.ids.New("workspace")
		workspaceID = core.WorkspaceID(raw)
	}
	if idErr == nil {
		agents := config.AgentSet{}
		if o.config.Agents != nil {
			agents = *o.config.Agents
		}
		agentIDs := restoreConfiguredAgentIDs(agents, o.config.AgentOrder, history.AgentIDs)
		o.workspace, idErr = newWorkspaceProjectionWithIDs(workspaceID, agents, o.config.AgentOrder, o.ids, agentIDs)
	}
	if idErr != nil {
		o.projectionError = idErr
		if logger != nil {
			logger.Error("initialize workspace projection", "error", idErr)
		}
	}
	if o.workspace != nil {
		switch {
		case options.Memory != nil:
			o.memory = options.Memory
		case options.MemoryFactory != nil:
			agentIDs := map[string]core.AgentID{}
			for _, name := range o.workspace.agentNamesSnapshot() {
				if id, ok := o.workspace.agentID(name); ok {
					agentIDs[name] = id
				}
			}
			o.memory = options.MemoryFactory(o.workspace.workspaceID(), agentIDs, o.memoryEvent)
		}
	}
	if o.workspace != nil {
		o.sessionTransport = newSessionTransport()
		o.delivery = newSessionDelivery(o.workspace, o.sessionTransport)
		o.delivery.memory = o.memory
		o.delivery.memoryEnabled = o.memory != nil
		o.delivery.shareGlobal = o.config.Memory.ShareGlobal
		router, err := comms.NewRouter(comms.RouterConfig{
			WorkspaceID: o.workspace.workspaceID(),
			Directory:   brokerDirectory{workspace: o.workspace, sessions: o.sessionTransport},
			Delivery:    o.delivery,
			IDs:         o.ids,
		})
		if err != nil {
			o.projectionError = errors.Join(o.projectionError, fmt.Errorf("initialize communication router: %w", err))
		} else {
			o.router = router
			o.brokerQueue = make(chan brokerCommand, 64)
		}
	}
	if o.trace == nil {
		w, err := ghosttrace.Open(cfg.Trace.Path, ghosttrace.Verbose(cfg.Trace.Verbose))
		o.trace = w
		if err != nil && logger != nil {
			logger.Error("open trace", "error", err)
		}
	}
	if cfg.QAC.Enabled {
		co, err := cognition.New(o.config.QAC)
		o.cognition = co
		if err != nil && logger != nil {
			logger.Error("disable invalid qac", "error", err)
		}
	}
	if cfg.QAC.Enabled {
		o.cesRequested, o.cesEffective = CESPolicyOn, CESPolicyOn
		if o.cognition == nil {
			o.cesEffective = CESPolicyOff
			o.cesPolicyReason = "unsupported: QAC not configured"
		}
	} else {
		o.cesRequested, o.cesEffective = CESPolicyOff, CESPolicyOff
	}
	if options.WorkDir != "" {
		log, path, err := openRunEventLog(options.WorkDir)
		if err != nil {
			o.eventLogError = err
			if logger != nil {
				logger.Error("open core event log", "error", err)
			}
		} else {
			o.eventLog, o.eventLogPath = log, path
		}
	}
	for stream, sequence := range history.LastSequences {
		if err := o.coreSequencer.Restore(stream, sequence); err != nil {
			o.projectionError = errors.Join(o.projectionError, err)
		}
		if o.eventLog != nil {
			if err := o.eventLog.RestoreSequence(stream, sequence); err != nil {
				o.eventLogError = errors.Join(o.eventLogError, err)
			}
		}
	}
	if o.workspace != nil {
		for _, item := range history.Events {
			if err := o.workspace.addEvent(item); err != nil {
				history.Warnings = append(history.Warnings, "event "+string(item.ID)+" could not be projected: "+err.Error())
			}
		}
	}
	o.recovery = history.Summary
	o.recovery.EventsRead = len(history.Events)
	if o.router != nil {
		if err := o.router.Rehydrate(history.Events); err != nil {
			history.Warnings = append(history.Warnings, "broker history could not be restored: "+err.Error())
			if logger != nil {
				logger.Error("restore communication history", "error", err)
			}
		} else {
			snapshot := o.router.Snapshot()
			o.brokerEventCount = len(snapshot.Events)
			o.workspace.replaceBrokerRecords(snapshot.Tasks, snapshot.Messages)
			for _, task := range snapshot.Tasks {
				if task.Status.IsTerminal() {
					o.memoryCaptured[task.ID] = true
				}
			}
			o.recovery.MessagesRestored = len(snapshot.Messages)
			o.recovery.TasksRestored = len(snapshot.Tasks)
			for _, task := range snapshot.Tasks {
				if task.CorrelationID != "" {
					o.taskCorrelation[task.ID] = task.CorrelationID
				}
			}
		}
	}
	var recoveryResult comms.RecoveryResult
	if o.router != nil {
		var err error
		recoveryResult, err = o.router.ReconcileAfterRestart(o.ctx)
		if err != nil {
			history.Warnings = append(history.Warnings, "broker reconciliation failed: "+err.Error())
		}
		o.recovery.ReconciledTasks = recoveryResult.ReconciledTasks
		o.recovery.UncertainDeliveries = recoveryResult.UncertainDeliveries
		o.recovery.PendingMessages = recoveryResult.PendingPureMessages
		o.syncBroker(o.router.Snapshot())
	}
	o.drainMemoryEvents()
	o.recordLostMemoryWrites(history.Events)
	if o.workspace != nil {
		o.recovery.Memory = memoryCountersForEvents(o.workspace.snapshot().Events)
	}
	if options.WorkDir != "" {
		if !history.HasWorkspaceIdentity {
			o.recordWorkspaceIdentity("workspace.initialized")
		} else {
			o.recordWorkspaceIdentity("workspace.identity_snapshot")
		}
		for _, warning := range history.Warnings {
			o.recordRecoveryWarning(warning)
		}
		o.recordRecoverySummary()
	}
	if o.router != nil {
		o.workers.Add(1)
		go o.brokerLoop()
	}
	if o.phaseRuntime == nil && cfg.Agents != nil {
		o.phaseRuntime = codex.NewRuntime()
	}
	go o.loop()
	return c
}

// loop is the only writer of the store, coordinator and trace. Each
// subscription has a private queue; a waiting client cannot block execution.
func (o *owner) loop() {
	defer close(o.controller.done)
	for {
		cases := []reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(o.controller.requests)},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(o.memoryEvents)},
		}
		ready := []*subscription{}
		for _, sub := range o.subscribers {
			if len(sub.queue) > 0 {
				cases = append(cases, reflect.SelectCase{Dir: reflect.SelectSend, Chan: reflect.ValueOf(sub.out), Send: reflect.ValueOf(sub.queue[0])})
				ready = append(ready, sub)
			}
		}
		index, value, _ := reflect.Select(cases)
		if index == 1 {
			o.publishCoreEvent(value.Interface().(core.Event))
			o.flushMemorySinkDrops()
			continue
		}
		if index > 1 {
			sub := ready[index-2]
			sub.queue[0] = Event{}
			sub.queue = sub.queue[1:]
			continue
		}
		r := value.Interface().(request)
		r.apply(o)
		close(r.done)
		if o.closing {
			for _, sub := range o.subscribers {
				close(sub.out)
			}
			return
		}
	}
}

func (c *Controller) call(f func(*owner)) error {
	r := request{apply: f, done: make(chan struct{})}
	select {
	case c.requests <- r:
	case <-c.done:
		return ErrClosed
	}
	<-r.done
	return nil
}

func (c *Controller) Status() Status {
	var status Status
	if err := c.call(func(o *owner) { status = o.status() }); err != nil {
		status.TraceError = err
	}
	return status
}
func (o *owner) status() Status {
	s := Status{Enabled: o.cognition != nil, Active: o.hasActiveRun(), Requested: o.cesRequested, Effective: o.cesEffective, Pending: o.cesPending, PolicyReason: o.cesPolicyReason, Resource: o.cesResource, PhaseRunID: o.cesRunID, TraceError: o.traceError, CoreError: o.projectionError, EventLogPath: o.eventLogPath, EventLogError: o.eventLogError, Recovery: o.recovery}
	if o.cesStore != nil {
		s.State = o.cesStore.State()
		s.View = o.cesStore.OperatorView()
	}
	if o.cognition != nil {
		s.Work = o.cognition.Work()
	}
	return s
}

// Subscribe delivers future updates in owner order. Call the returned function
// to release the subscription. Replay and bounded transport queues are outside
// this extraction.
func (c *Controller) Subscribe() (<-chan Event, func()) {
	sub := &subscription{out: make(chan Event)}
	if err := c.call(func(o *owner) { o.subscribers = append(o.subscribers, sub) }); err != nil {
		close(sub.out)
	}
	var once sync.Once
	return sub.out, func() {
		once.Do(func() {
			_ = c.call(func(o *owner) {
				for i, s := range o.subscribers {
					if s == sub {
						o.subscribers = append(o.subscribers[:i], o.subscribers[i+1:]...)
						close(sub.out)
						return
					}
				}
			})
		})
	}
}
func (o *owner) emit(e Event) {
	for _, sub := range o.subscribers {
		sub.queue = append(sub.queue, cloneEvent(e))
	}
}
func (o *owner) publishBoundary() { s := o.status(); o.emit(Event{Status: &s, Boundary: true}) }

func (c *Controller) Start(goal string) error {
	var err error
	var brokerDone <-chan error
	callErr := c.call(func(o *owner) {
		brokerDone, err = o.start(goal, nil)
		o.publishBoundary()
	})
	if brokerDone != nil {
		err = errors.Join(err, <-brokerDone)
	}
	return errors.Join(err, callErr)
}

// StartWithPolicy starts one task with a local CES policy override. It does
// not change the workspace default set by SetCESPolicy.
func (c *Controller) StartWithPolicy(goal string, policy CESPolicy) error {
	if !validCESPolicy(policy) {
		return fmt.Errorf("unknown CES policy %q", policy)
	}
	var err error
	var brokerDone <-chan error
	callErr := c.call(func(o *owner) {
		brokerDone, err = o.start(goal, &policy)
		o.publishBoundary()
	})
	if brokerDone != nil {
		err = errors.Join(err, <-brokerDone)
	}
	return errors.Join(err, callErr)
}

func (o *owner) start(goal string, override *CESPolicy) (<-chan error, error) {
	switch {
	case o.stopping:
		return nil, ErrClosed
	case o.traceError != nil:
		return nil, o.traceError
	case o.hasActiveRun():
		return nil, errors.New("work is active")
	}
	if o.cesPending {
		o.applyRequestedCESPolicy()
	}
	policy := o.cesEffective
	if override != nil {
		policy = *override
	}
	if policy == CESPolicyOn && o.cognition == nil {
		policy = CESPolicyOff
	}
	if policy == CESPolicyOn {
		o.startCESWork(goal)
		return nil, nil
	}
	return o.startStandardWork(goal)
}

// SetCESPolicy requests the workspace CES default. A live provider turn keeps
// its current policy until the next Start boundary.
func (c *Controller) SetCESPolicy(policy CESPolicy) error {
	if !validCESPolicy(policy) {
		return fmt.Errorf("unknown CES policy %q", policy)
	}
	return c.call(func(o *owner) {
		o.requestCESPolicy(policy)
		o.publishBoundary()
	})
}

// SetEventLogWorkDir selects the workspace root for this run's durable log.
// A log error is retained in Status and does not prevent execution.
func (c *Controller) SetEventLogWorkDir(workdir string) error {
	var result error
	callErr := c.call(func(o *owner) {
		if workdir == "" {
			result = errors.New("work directory is required")
			return
		}
		if o.eventLog != nil && filepath.Dir(o.eventLogPath) == filepath.Join(workdir, ".ghost", "events") {
			return
		}
		log, path, err := openRunEventLog(workdir)
		if err != nil {
			result = err
			o.eventLogError = err
			if o.eventLog != nil {
				result = errors.Join(result, o.eventLog.Close())
				o.eventLog = nil
				o.eventLogPath = ""
			}
			return
		}
		if o.workspace != nil {
			for _, item := range o.workspace.snapshot().Events {
				if err := log.RestoreSequence(item.StreamKey(), item.Seq); err != nil {
					result = errors.Join(result, err)
					_ = log.Close()
					return
				}
			}
		}
		if o.eventLog != nil {
			result = o.eventLog.Close()
		}
		o.eventLog, o.eventLogPath = log, path
		o.eventLogError = result
	})
	return errors.Join(result, callErr)
}

// Send records one human message and routes only to its resolved recipients.
// If no task is active, the message starts a new run with the same goal.
func (c *Controller) Send(recipients []string, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("message text is required")
	}
	recipients = append([]string(nil), recipients...)
	var result error
	var brokerDone <-chan error
	callErr := c.call(func(o *owner) {
		if o.stopping {
			result = ErrClosed
			return
		}
		ids, broadcast, err := o.workspace.resolveRecipients(recipients, text)
		if err != nil {
			result = err
			return
		}
		if o.hasActiveRun() {
			if len(ids) == 0 && !broadcast {
				task, ok := o.workspace.task(o.activeTaskID)
				if !ok {
					result = errors.New("active run has no assigned agent")
					return
				}
				ids = []core.AgentID{task.Owner}
			}
			message, err := o.humanMessage(text, ids, broadcast, o.activeTaskID)
			if err != nil {
				result = err
				return
			}
			brokerDone = o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
				_, err := router.Send(o.ctx, message)
				return err
			}})
		} else {
			o.startRecipients, o.startBroadcast = ids, broadcast
			brokerDone, result = o.start(text, nil)
			o.startRecipients, o.startBroadcast = nil, false
		}
		o.publishBoundary()
	})
	if brokerDone != nil {
		result = errors.Join(result, <-brokerDone)
	}
	return errors.Join(result, callErr)
}

// Steer preserves the current rule: record global input during CES work;
// address the persistent owner session only on the older non-CES path.
func (c *Controller) Steer(text string) error {
	var result error
	var brokerDone <-chan error
	callErr := c.call(func(o *owner) {
		if o.stopping {
			result = ErrClosed
			return
		}
		o.appendEvent(event.Event{Source: "SYSTEM", Kind: event.KindSteering, Message: "global steering updated: " + text})
		if o.cesActive() {
			o.appendEvent(event.Event{Source: "CES", Kind: event.KindSteering, Message: "steering recorded during " + strings.ToUpper(string(o.cesStore.State().Task.Phase))})
		} else if task, ok := o.workspace.task(o.activeTaskID); ok && !task.Status.IsTerminal() {
			message, err := o.humanMessage(text, []core.AgentID{task.Owner}, false, task.ID)
			if err != nil {
				result = err
			} else {
				brokerDone = o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
					_, err := router.Send(o.ctx, message)
					return err
				}})
			}
		} else if o.cognition != nil && o.cognition.Work() != nil && o.cognition.Work().State == cognition.WorkActive {
			work := o.cognition.Work()
			if s := o.sessions[work.OwnerAgent]; s != nil {
				o.cognition.ExpectTurn(work.OwnerAgent)
				o.sendPersistent(work.OwnerAgent, s, text)
			}
		} else {
			brokerDone, result = o.start(text, nil)
		}
		o.publishBoundary()
	})
	if brokerDone != nil {
		result = errors.Join(result, <-brokerDone)
	}
	return errors.Join(result, callErr)
}

// Cancel stops the active phase and records an operator cancellation.
func (c *Controller) Cancel() error {
	var brokerDone <-chan error
	callErr := c.call(func(o *owner) {
		if o.cancel != nil {
			o.cancel()
		}
		if task, ok := o.workspace.task(o.activeTaskID); ok && !task.Status.IsTerminal() && o.router != nil {
			id := task.ID
			brokerDone = o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
				return router.Cancel(o.ctx, id, "operator canceled the run")
			}})
		}
		if o.cesActive() {
			o.cesTerminal(epistemic.WorkIncomplete, "operator_canceled", "operator canceled the run")
		} else if task, ok := o.workspace.task(o.activeTaskID); ok && !task.Status.IsTerminal() {
			for name, session := range o.sessions {
				if session != nil && (session.State() == runtime.StateRunning || session.State() == runtime.StateInterrupting) {
					o.interruptPersistent(name, session)
				}
			}
			if brokerDone == nil {
				o.transitionProjectedTask(core.TaskCancelled, "operator_canceled")
			}
		}
		o.denyPendingApprovals("canceled")
		o.publishBoundary()
	})
	if brokerDone != nil {
		return errors.Join(callErr, <-brokerDone)
	}
	return callErr
}

// CancelTask cancels one routed task and all open descendants.
func (c *Controller) CancelTask(id core.TaskID) error {
	var brokerDone <-chan error
	callErr := c.call(func(o *owner) {
		if o.stopping || o.router == nil {
			brokerDone = closedBrokerResult(ErrClosed)
			return
		}
		if task, ok := o.workspace.task(id); ok && task.Parent != nil {
			if parent := o.taskPendingAgents[*task.Parent]; parent != nil {
				if name, exists := o.workspace.agentName(task.Requester); exists {
					parent[name] = true
				}
			}
		}
		brokerDone = o.queueBroker(brokerCommand{apply: func(router *comms.Router) error {
			return router.Cancel(o.ctx, id, "operator canceled delegated work")
		}})
	})
	if brokerDone != nil {
		return errors.Join(callErr, <-brokerDone)
	}
	return callErr
}

func closedBrokerResult(err error) <-chan error {
	done := make(chan error, 1)
	done <- err
	return done
}

func (c *Controller) Close() error {
	c.closeOnce.Do(func() {
		var o *owner
		_ = c.call(func(current *owner) {
			o = current
			o.stopping = true
			o.stop()
			if o.cancel != nil {
				o.cancel()
			}
		})
		if o == nil {
			return
		}
		// Keep the owner loop available until the worker has delivered its last event.
		o.workers.Wait()
		if o.memory != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			closeResult, memoryErr := o.memory.Close(ctx)
			cancel()
			if memoryErr != nil && closeResult.Lost == 0 {
				c.closeErr = errors.Join(c.closeErr, memoryErr)
			}
		}
		_ = c.call(func(o *owner) {
			o.drainMemoryEvents()
			if o.phaseRuntime != nil {
				c.closeErr = errors.Join(c.closeErr, o.phaseRuntime.Close())
			}
			c.closeErr = errors.Join(c.closeErr, o.trace.Close())
			c.closeErr = errors.Join(c.closeErr, o.eventLog.Close())
			o.closing = true
		})
		<-c.done
	})
	return c.closeErr
}

func (o *owner) record(agent string, item event.Event, meta map[string]string) {
	item = o.writeTrace(agent, item, meta)
	coreEvent := o.projectLegacy(agent, item)
	o.emit(Event{AgentID: agent, Entry: item, CoreEvent: coreEvent})
}

// writeTrace is the only trace writer. record publishes the same event;
// Trace does not, so a caller that is already projecting does not see a second copy.
func (o *owner) writeTrace(agent string, item event.Event, meta map[string]string) event.Event {
	if item.Time.IsZero() {
		item.Time = timeNow()
	}
	item.Metadata = event.MergeMeta(item.Metadata, meta)
	if agent != "" {
		item.Metadata["agent_id"] = agent
	}
	if o.trace != nil {
		if err := o.trace.Write(item); err != nil {
			o.failTrace(err)
		}
	}
	return item
}
func (o *owner) appendEvent(e event.Event) { o.record("", e, nil) }
func (c *Controller) Record(agent string, e event.Event, meta map[string]string) error {
	e = clone(e)
	meta = clone(meta)
	return c.call(func(o *owner) { o.record(agent, e, meta); o.publishBoundary() })
}
func (o *owner) publishCES() {
	if o.cesStore == nil {
		return
	}
	s := o.status()
	o.emit(Event{Status: &s})
	semantic := o.cesStore.SemanticEvents()
	start := o.cesPublished
	if start > len(semantic) {
		start = len(semantic)
	}
	for _, item := range semantic[start:] {
		v := item
		entry := cesSemanticEvent(item, s.State, s.View)
		entry = o.writeTrace("", entry, map[string]string{"semantic_kind": string(item.Kind)})
		coreEvent := o.projectLegacy("", entry)
		o.emit(Event{Entry: entry, CoreEvent: coreEvent, Semantic: &v})
	}
	o.cesPublished = len(semantic)
}
func (o *owner) setCESResourceStatus(state epistemic.WorkStatus, activity string) {
	agent, _ := o.cognition.Agent(o.cesResource)
	o.emit(Event{AgentID: agent, ResourceStatus: state, Activity: activity})
}
func artifactValidator(store *epistemic.Store, phase epistemic.Phase, resource string, projection epistemic.Projection) func(cognition.PhaseArtifact) error {
	return func(artifact cognition.PhaseArtifact) error {
		producer := epistemic.Producer{Phase: phase, Process: resource, Artifact: string(phase) + "_artifact"}
		request, err := commitRequestForArtifact(store, phase, resource, artifact, projection, producer)
		if err != nil {
			return cognition.RepairableArtifactError(err)
		}
		if cesDeltaEmpty(request.Delta) {
			return nil
		}
		if err = store.ValidateCommit(request); epistemic.IsRepairableCommitError(err) {
			return cognition.RepairableArtifactError(err)
		}
		return err
	}
}
func clone[T any](value T) T {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var copy T
	if err = json.Unmarshal(data, &copy); err != nil {
		panic(err)
	}
	return copy
}
func cloneEvent(e Event) Event {
	var traceErr, coreErr, eventLogErr error
	if e.Status != nil {
		traceErr = e.Status.TraceError
		coreErr = e.Status.CoreError
		eventLogErr = e.Status.EventLogError
		s := *e.Status
		s.TraceError, s.CoreError, s.EventLogError = nil, nil, nil
		e.Status = &s
	}
	e = clone(e)
	if e.Status != nil {
		e.Status.TraceError, e.Status.CoreError, e.Status.EventLogError = traceErr, coreErr, eventLogErr
	}
	return e
}
func clonePhaseEvent(e cognition.PhaseEvent) cognition.PhaseEvent { return clone(e) }

var timeNow = time.Now

// Trace writes a client event through writeTrace without echoing an event the
// caller is already projecting.
func (c *Controller) Trace(agent string, item event.Event, meta map[string]string) error {
	item = clone(item)
	meta = clone(meta)
	return c.call(func(o *owner) { o.writeTrace(agent, item, meta) })
}

// Copy structs by value to retain parsed, private timing fields.
func cloneConfig(cfg config.Config) config.Config {
	cfg.AgentOrder = append([]string(nil), cfg.AgentOrder...)
	if cfg.Agents != nil {
		agents := config.AgentSet{}
		for id, backend := range *cfg.Agents {
			agents[id] = backend
		}
		cfg.Agents = &agents
	}
	cfg.QAC.Policy.Hierarchy = append([]string(nil), cfg.QAC.Policy.Hierarchy...)
	resources := map[string]config.QACResourceConfig{}
	for id, resource := range cfg.QAC.Resources {
		if limit := resource.Budget.MaxActivationsPerRun; limit != nil {
			value := *limit
			resource.Budget.MaxActivationsPerRun = &value
		}
		resources[id] = resource
	}
	cfg.QAC.Resources = resources
	return cfg
}
