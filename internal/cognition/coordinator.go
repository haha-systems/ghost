package cognition

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/qac"
	"github.com/haha-systems/qac/policy/threshold"
)

type binding struct {
	id, agent                  string
	capability, cost, scarcity float64
	max                        *int
	cooldown                   time.Duration
}

type budgetState struct {
	activations int
	last        time.Time
}

type turnKey struct{ agent, turn string }

type pendingDispatch struct {
	planID, agent string
	awaitingTurn  bool
}

type Snapshot struct {
	Resources map[string]qac.Resource
	Budget    qac.BudgetState
}

type Coordinator struct {
	policy     qac.Policy
	entry      string
	importance float64
	bindings   map[string]binding
	work       *WorkItem
	budgets    map[string]budgetState
	next       int
	turns      map[turnKey]bool
	output     map[turnKey]string
	pending    *pendingDispatch
	jev        JevClient
	jevTimeout time.Duration
}

// SetJev installs an optional bounded cognitive-adequacy assessor powered
// by Jev. A nil assessor preserves the pre-Jev allocation path.
func (c *Coordinator) SetJev(jev JevClient) { c.jev = jev }

// SetJevTimeout bounds an optional assessor. Invalid values retain the
// default so Jev cannot block the existing QAC path indefinitely.
func (c *Coordinator) SetJevTimeout(timeout time.Duration) {
	if timeout > 0 {
		c.jevTimeout = timeout
	}
}

// WillAssessJev reports whether this allocation can call the optional client.
func (c *Coordinator) WillAssessJev(situation CognitiveSituation) bool {
	return c.jev != nil && shouldAssessJev(situation)
}

func New(cfg config.QACConfig) (*Coordinator, error) {
	p, e := threshold.New(threshold.Config{Hierarchy: cfg.Policy.Hierarchy})

	if e != nil {
		return nil, e
	}

	c := &Coordinator{
		policy:     p,
		entry:      cfg.EntryResource,
		importance: cfg.DefaultImportance,
		bindings:   map[string]binding{},
		budgets:    map[string]budgetState{},
		turns:      map[turnKey]bool{},
		output:     map[turnKey]string{},
		jevTimeout: 5 * time.Second,
	}

	for id, r := range cfg.Resources {
		c.bindings[id] = binding{
			id:         id,
			agent:      r.Agent,
			capability: r.Capability,
			cost:       r.Cost,
			scarcity:   r.Scarcity,
			max:        r.Budget.MaxActivationsPerRun,
			cooldown:   r.Budget.Cooldown(),
		}
	}

	return c, nil
}

func (c *Coordinator) TrackTurn(agent, turn string) {
	if turn == "" || (c.work != nil && c.work.State != WorkActive) {
		return
	}
	allowed := c.pending != nil && c.pending.awaitingTurn && c.pending.agent == agent
	if c.pending == nil && c.work != nil && c.work.OwnerAgent == agent {
		allowed = true
	}
	if allowed {
		c.turns[turnKey{agent: agent, turn: turn}] = true
	}
}

func (c *Coordinator) BeginDispatch(p Plan) error {
	b, ok := c.bindings[p.To]
	if !ok {
		return fmt.Errorf("unknown resource %q", p.To)
	}

	if p.Action == qac.ActionEscalate || p.Action == qac.ActionRelease || p.Initial || p.Action == qac.ActionContinue {
		if c.pending != nil && c.pending.planID != p.ID {
			return fmt.Errorf("transition is pending")
		}
		c.pending = &pendingDispatch{planID: p.ID, agent: b.agent, awaitingTurn: true}
	}

	return nil
}

func (c *Coordinator) Fail(p Plan) {
	if c.pending != nil && c.pending.planID == p.ID {
		c.pending = nil
	}
}
func (c *Coordinator) ExpectTurn(agent string) {
	c.pending = &pendingDispatch{agent: agent, awaitingTurn: true}
}

func (c *Coordinator) Observe(ctx context.Context, e runtime.Event, sessions map[string]runtime.Session, now time.Time) (*Plan, error) {
	if e.Kind == runtime.KindStatus && e.Metadata["backend_method"] == "turn/started" && c.pending != nil && e.AgentID == c.pending.agent && e.TurnID != "" {
		c.TrackTurn(e.AgentID, e.TurnID)
		c.pending.awaitingTurn = false
	}

	key := turnKey{agent: e.AgentID, turn: e.TurnID}

	if !c.turns[key] {
		return nil, nil
	}

	if e.Kind == runtime.KindMessage && strings.Contains(e.Metadata["backend_method"], "delta") {
		c.output[key] += e.Summary
		return nil, nil
	}

	if e.Kind == runtime.KindStatus && (e.Metadata["backend_method"] == "turn/completed" || e.Metadata["backend_method"] == "turn/failed") {
		delete(c.turns, key)
		text := c.output[key]
		delete(c.output, key)
		c.pending = nil
		if e.Metadata["backend_method"] == "turn/failed" {
			return nil, nil
		}
		return c.decide(ctx, text, sessions, now)
	}
	return nil, nil
}

func (c *Coordinator) decide(ctx context.Context, text string, sessions map[string]runtime.Session, now time.Time) (*Plan, error) {
	request, visible, found, err := ParseQACRequest(text)
	if err != nil || !found {
		return nil, err
	}

	plan, err := c.Allocate(ctx, request, sessions, now)
	if err != nil {
		return nil, err
	}

	plan.Visible = visible

	return &plan, nil
}

// Allocate selects a resource for the current phase. It does not choose or
// advance the epistemic phase and it does not set work completion.
func (c *Coordinator) Allocate(ctx context.Context, request QACRequest, sessions map[string]runtime.Session, now time.Time) (Plan, error) {
	return c.AllocateWithSituation(ctx, request, CognitiveSituation{}, sessions, now)
}

// AllocateWithSituation lets QAC consider a validated Jev judgment while QAC
// retains deterministic ownership of the resource decision.
func (c *Coordinator) AllocateWithSituation(ctx context.Context, request QACRequest, situation CognitiveSituation, sessions map[string]runtime.Session, now time.Time) (Plan, error) {
	if c.work == nil || c.work.State != WorkActive {
		return Plan{}, fmt.Errorf("no active work to allocate")
	}

	request.FailedAttempts = max(request.FailedAttempts, situation.FailedAttempts)
	assessment := c.assessJev(ctx, request, situation)

	if assessment != nil && assessment.Judgment != nil {
		if adjusted, used := applyJevAdvice(request, *assessment.Judgment); used {
			request = adjusted
			assessment.Used = true
		}
	}

	snapshot := c.Snapshot(sessions, now)
	ids := make([]string, 0, len(snapshot.Resources))

	for id := range snapshot.Resources {
		ids = append(ids, id)
	}

	sort.Strings(ids)
	resources := make([]qac.Resource, 0, len(ids))

	for _, id := range ids {
		resources = append(resources, snapshot.Resources[id])
	}

	decision, err := c.policy.Decide(ctx, qac.Request{
		Context: qac.Context{
			CurrentResource: c.work.OwnerResource,
			Uncertainty:     request.Uncertainty,
			Importance:      c.work.Importance,
			Novelty:         request.Novelty,
			ExpectedGain:    request.ExpectedGain,
			FailedAttempts:  request.FailedAttempts,
		},
		Resources: resources,
		Budget:    snapshot.Budget,
	})
	if err != nil {
		return Plan{}, err
	}

	c.next++

	return Plan{ID: fmt.Sprintf("plan-%d", c.next), From: decision.From, To: decision.To, WorkID: c.work.ID, Action: decision.Action, Decision: decision, Request: request, Jev: assessment}, nil
}

func (c *Coordinator) assessJev(ctx context.Context, request QACRequest, situation CognitiveSituation) *JevAssessment {
	if !c.WillAssessJev(situation) {
		return nil
	}

	input := situation.jevInput(c.work.OwnerResource)
	input.FailedAttempts = max(input.FailedAttempts, request.FailedAttempts)
	assessment := &JevAssessment{Requested: true, Input: input}
	assessCtx, cancel := context.WithTimeout(ctx, c.jevTimeout)
	defer cancel()

	raw, err := c.jev.Assess(assessCtx, input)
	if err != nil {
		assessment.IgnoreReason = "unavailable"
		return assessment
	}

	judgment, err := ParseJevJudgment(raw)
	if err != nil {
		assessment.IgnoreReason = "malformed"
		return assessment
	}

	assessment.Judgment = &judgment
	if judgment.Judgment == JevInsufficientEvidence {
		assessment.IgnoreReason = "insufficient_evidence"
	}

	return assessment
}
func (c *Coordinator) Work() *WorkItem {
	if c.work == nil {
		return nil
	}

	v := *c.work

	return &v
}

// SyncWorkStatus mirrors a status owned by CES into the resource-allocation
// view. QAC decisions do not call this method.
func (c *Coordinator) SyncWorkStatus(status WorkState, reason string, now time.Time) {
	if c.work == nil {
		return
	}
	c.work.State = status
	c.work.TerminalReason = reason
	c.work.UpdatedAt = now
}

func (c *Coordinator) Activations(id string) int { return c.budgets[id].activations }

func (c *Coordinator) Agent(id string) (string, bool) { b, ok := c.bindings[id]; return b.agent, ok }

func (c *Coordinator) StartWork(goal string) (Plan, error) {
	if c.work != nil && c.work.State == WorkActive {
		return Plan{}, fmt.Errorf("work is already active")
	}

	b, ok := c.bindings[c.entry]
	if !ok {
		return Plan{}, fmt.Errorf("entry resource %q is unavailable", c.entry)
	}

	c.next++

	return Plan{ID: fmt.Sprintf("plan-%d", c.next), To: b.id, Goal: goal, Initial: true}, nil
}
func (c *Coordinator) Commit(p Plan, now time.Time) error {
	if !p.Initial && p.Action == qac.ActionStop {
		if c.work == nil || c.work.ID != p.WorkID {
			return fmt.Errorf("stale plan")
		}

		c.pending = nil

		return nil
	}

	b, ok := c.bindings[p.To]
	if !ok {
		return fmt.Errorf("unknown resource %q", p.To)
	}

	if p.Initial {
		c.next++
		c.work = &WorkItem{ID: fmt.Sprintf("work-%d", c.next), Goal: p.Goal, Importance: c.importance, OwnerResource: b.id, OwnerAgent: b.agent, State: WorkActive, CreatedAt: now, UpdatedAt: now}
		p.WorkID = c.work.ID
	} else if c.work == nil || c.work.ID != p.WorkID {
		return fmt.Errorf("stale plan")
	}

	if !p.Initial && c.work.State != WorkActive {
		return fmt.Errorf("work is terminal")
	}

	if !p.Initial {
		switch p.Action {
		case qac.ActionContinue:
			return nil
		case qac.ActionEscalate, qac.ActionRelease:
			if c.pending != nil && c.pending.planID != p.ID {
				return fmt.Errorf("stale plan")
			}
			c.work.OwnerResource = b.id
			c.work.OwnerAgent = b.agent
			c.work.Handoffs++
			c.work.UpdatedAt = now
		default:
			return fmt.Errorf("unknown qac action %q", p.Action)
		}
	}

	s := c.budgets[b.id]
	s.activations++
	s.last = now
	c.budgets[b.id] = s
	return nil
}
func (c *Coordinator) Snapshot(sessions map[string]runtime.Session, now time.Time) Snapshot {
	out := Snapshot{
		Resources: map[string]qac.Resource{},
		Budget: qac.BudgetState{
			Resources: map[string]qac.ResourceBudget{},
		},
	}

	for id, b := range c.bindings {
		s := sessions[b.agent]
		available := s != nil && s.State() == runtime.StateIdle

		if c.work != nil && c.work.OwnerResource == id {
			available = true
		}

		out.Resources[id] = qac.Resource{ID: id, Capability: b.capability, Cost: b.cost, Scarcity: b.scarcity, Available: available}
		state := c.budgets[id]

		if b.max != nil || b.cooldown > 0 {
			budget := qac.ResourceBudget{Enabled: true}

			if b.max != nil {
				n := *b.max - state.activations
				budget.RemainingInvocations = &n
			}

			budget.Cooldown = b.cooldown > 0 && now.Before(state.last.Add(b.cooldown))
			out.Budget.Resources[id] = budget
		}
	}

	return out
}
