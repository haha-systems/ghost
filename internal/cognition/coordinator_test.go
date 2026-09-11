package cognition

import (
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	"testing"
	"time"
)

func testConfig() config.QACConfig {
	return config.QACConfig{Enabled: true, EntryResource: "wraith", DefaultImportance: .5, Policy: config.QACPolicyConfig{Type: "threshold", Hierarchy: []string{"wraith", "shade", "veil"}}, Resources: map[string]config.QACResourceConfig{"wraith": {Agent: "wraith", Capability: .3, Cost: .1, Scarcity: .05}, "shade": {Agent: "shade", Capability: .65, Cost: .3, Scarcity: .3}, "veil": {Agent: "veil", Capability: .95, Cost: .8, Scarcity: .95, Budget: config.QACBudgetConfig{MaxActivationsPerRun: intp(1)}}}}
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
