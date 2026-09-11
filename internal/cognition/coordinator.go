package cognition

import (
	"context"
	"fmt"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/qac"
	"github.com/haha-systems/qac/policy/threshold"
	"sort"
	"strings"
	"time"
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
	turns      map[string]bool
	output     map[string]string
	pending    string
	awaitAgent string
}

func New(cfg config.QACConfig) (*Coordinator, error) {
	p, e := threshold.New(threshold.Config{Hierarchy: cfg.Policy.Hierarchy})
	if e != nil {
		return nil, e
	}
	c := &Coordinator{policy: p, entry: cfg.EntryResource, importance: cfg.DefaultImportance, bindings: map[string]binding{}, budgets: map[string]budgetState{}, turns: map[string]bool{}, output: map[string]string{}}
	for id, r := range cfg.Resources {
		c.bindings[id] = binding{id: id, agent: r.Agent, capability: r.Capability, cost: r.Cost, scarcity: r.Scarcity, max: r.Budget.MaxActivationsPerRun, cooldown: r.Budget.Cooldown()}
	}
	return c, nil
}
func (c *Coordinator) TrackTurn(agent, turn string) {
	if c.work != nil && c.work.State == WorkActive && (c.work.OwnerAgent == agent || c.awaitAgent == agent) && turn != "" {
		c.turns[agent+"\x00"+turn] = true
	}
}
func (c *Coordinator) BeginDispatch(p Plan) error {
	b, ok := c.bindings[p.To]
	if !ok {
		return fmt.Errorf("unknown resource %q", p.To)
	}
	if p.Action == qac.ActionEscalate || p.Action == qac.ActionRelease || p.Initial {
		if c.pending != "" && c.pending != p.ID {
			return fmt.Errorf("transition is pending")
		}
		c.pending = p.ID
		c.awaitAgent = b.agent
	}
	if p.Action == qac.ActionContinue {
		c.awaitAgent = b.agent
	}
	return nil
}
func (c *Coordinator) Fail(p Plan) {
	if c.pending == p.ID {
		c.pending = ""
		c.awaitAgent = ""
	}
}
func (c *Coordinator) Observe(ctx context.Context, e runtime.Event, sessions map[string]runtime.Session, now time.Time) (*Plan, error) {
	if e.Kind == runtime.KindStatus && e.Summary == "turn started" && e.AgentID == c.awaitAgent && e.TurnID != "" {
		c.TrackTurn(e.AgentID, e.TurnID)
		c.awaitAgent = ""
	}
	key := e.AgentID + "\x00" + e.TurnID
	if !c.turns[key] {
		return nil, nil
	}
	if e.Kind == runtime.KindMessage && strings.Contains(e.Metadata["backend_method"], "delta") {
		c.output[key] += e.Summary
		return nil, nil
	}
	if e.Kind == runtime.KindStatus && e.Metadata["backend_method"] == "turn/completed" {
		delete(c.turns, key)
		text := c.output[key]
		delete(c.output, key)
		return c.decide(ctx, text, sessions, now)
	}
	return nil, nil
}
func (c *Coordinator) decide(ctx context.Context, text string, sessions map[string]runtime.Session, now time.Time) (*Plan, error) {
	request, _, found, err := ParseQACRequest(text)
	if err != nil || !found {
		return nil, err
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
	decision, err := c.policy.Decide(ctx, qac.Request{Context: qac.Context{CurrentResource: c.work.OwnerResource, Uncertainty: request.Uncertainty, Importance: c.work.Importance, Novelty: request.Novelty, ExpectedGain: request.ExpectedGain, FailedAttempts: request.FailedAttempts}, Resources: resources, Budget: snapshot.Budget})
	if err != nil {
		return nil, err
	}
	c.next++
	return &Plan{ID: fmt.Sprintf("plan-%d", c.next), From: decision.From, To: decision.To, WorkID: c.work.ID, Action: decision.Action, Decision: decision, Request: request}, nil
}
func (c *Coordinator) Work() *WorkItem {
	if c.work == nil {
		return nil
	}
	v := *c.work
	return &v
}
func (c *Coordinator) Activations(id string) int      { return c.budgets[id].activations }
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
	if !p.Initial {
		switch p.Action {
		case qac.ActionContinue:
			return nil
		case qac.ActionStop:
			c.work.State = WorkPaused
			c.work.UpdatedAt = now
			c.pending = ""
			return nil
		case qac.ActionEscalate, qac.ActionRelease:
			if c.pending != "" && c.pending != p.ID {
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
	c.pending = ""
	return nil
}
func (c *Coordinator) Snapshot(sessions map[string]runtime.Session, now time.Time) Snapshot {
	out := Snapshot{Resources: map[string]qac.Resource{}, Budget: qac.BudgetState{Resources: map[string]qac.ResourceBudget{}}}
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
