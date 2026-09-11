package cognition

import (
	"fmt"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/qac"
	"github.com/haha-systems/qac/policy/threshold"
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
}

func New(cfg config.QACConfig) (*Coordinator, error) {
	p, e := threshold.New(threshold.Config{Hierarchy: cfg.Policy.Hierarchy})
	if e != nil {
		return nil, e
	}
	c := &Coordinator{policy: p, entry: cfg.EntryResource, importance: cfg.DefaultImportance, bindings: map[string]binding{}, budgets: map[string]budgetState{}}
	for id, r := range cfg.Resources {
		c.bindings[id] = binding{id: id, agent: r.Agent, capability: r.Capability, cost: r.Cost, scarcity: r.Scarcity, max: r.Budget.MaxActivationsPerRun, cooldown: r.Budget.Cooldown()}
	}
	return c, nil
}
func (c *Coordinator) Work() *WorkItem {
	if c.work == nil {
		return nil
	}
	v := *c.work
	return &v
}
func (c *Coordinator) Activations(id string) int { return c.budgets[id].activations }
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
	s := c.budgets[b.id]
	s.activations++
	s.last = now
	c.budgets[b.id] = s
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
