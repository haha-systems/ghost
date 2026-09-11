package cognition

import (
	"context"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	"github.com/haha-systems/qac"
	"testing"
	"time"
)

func testConfig() config.QACConfig {
	return config.QACConfig{Enabled: true, EntryResource: "wraith", DefaultImportance: .5, Policy: config.QACPolicyConfig{Type: "threshold", Hierarchy: []string{"wraith", "shade", "veil"}}, Resources: map[string]config.QACResourceConfig{"wraith": {Agent: "wraith", Capability: .3, Cost: .1, Scarcity: .05}, "shade": {Agent: "shade", Capability: .65, Cost: .3, Scarcity: .3}, "veil": {Agent: "veil", Capability: .95, Cost: .8, Scarcity: .95, Budget: config.QACBudgetConfig{MaxActivationsPerRun: intp(1)}}}}
}

func TestObserveCompletedManagedTurnEscalates(t *testing.T) {
	c, _ := New(testConfig())
	p, _ := c.StartWork("x")
	_ = c.Commit(p, time.Now())
	c.TrackTurn("wraith", "t1")
	_, err := c.Observe(context.Background(), runtime.Event{AgentID: "wraith", TurnID: "t1", Kind: runtime.KindMessage, Summary: "<QAC_REQUEST>{\"direction\":\"escalate\",\"uncertainty\":0.9,\"novelty\":0.5,\"expected_gain\":0.9,\"failed_attempts\":2}</QAC_REQUEST>", Metadata: map[string]string{"backend_method": "item/agentMessage/delta"}}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.Observe(context.Background(), runtime.Event{AgentID: "wraith", TurnID: "t1", Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}}, map[string]runtime.Session{"shade": fake.NewSession("shade")}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.Action != qac.ActionEscalate || plan.To != "shade" {
		t.Fatalf("plan=%#v", plan)
	}
}
func intp(v int) *int { return &v }
func TestStartWorkCommitsEntryActivation(t *testing.T) {
	c, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.StartWork("fix race")
	if err != nil {
		t.Fatal(err)
	}
	if p.To != "wraith" || c.Work() != nil {
		t.Fatalf("plan=%#v work=%#v", p, c.Work())
	}
	if err := c.Commit(p, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := c.Work(); got == nil || got.OwnerResource != "wraith" || got.Goal != "fix race" {
		t.Fatalf("work=%#v", got)
	}
	if c.Activations("wraith") != 1 {
		t.Fatal("entry activation not committed")
	}
}
func TestSnapshotRejectsBusyDestinationAndAllowsOwner(t *testing.T) {
	c, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.StartWork("x")
	_ = c.Commit(p, time.Now())
	w := fake.NewSession("wraith")
	s := fake.NewSession("shade")
	_ = s.Send(t.Context(), runtime.Input{Text: "manual"})
	snapshot := c.Snapshot(map[string]runtime.Session{"wraith": w, "shade": s, "veil": fake.NewSession("veil")}, time.Now())
	if !snapshot.Resources["wraith"].Available || snapshot.Resources["shade"].Available {
		t.Fatalf("resources=%#v", snapshot.Resources)
	}
}
