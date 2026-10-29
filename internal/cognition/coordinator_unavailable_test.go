package cognition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	"github.com/haha-systems/qac"
)

// abduceRelease is the request from Mark's run after TRIAGE.
var abduceRelease = QACRequest{Direction: "release", Uncertainty: .68, Novelty: .10, ExpectedGain: .80}

func allocateWith(t *testing.T, cfg config.QACConfig, wraith runtime.Session) Plan {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.StartWork("x")
	if err := c.Commit(p, time.Now()); err != nil {
		t.Fatal(err)
	}
	sessions := map[string]runtime.Session{"shade": fake.NewSession("shade"), "veil": fake.NewSession("veil")}
	if wraith != nil {
		sessions["wraith"] = wraith
	}
	plan, err := c.Allocate(context.Background(), abduceRelease, sessions, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func wraithIn(state runtime.SessionState) runtime.Session {
	s := fake.NewSession("wraith")
	switch state {
	case runtime.StateRunning:
		_ = s.Guard.StartTurn("t1")
	case runtime.StateStopped:
		s.Guard.Stop()
	case runtime.StateFailed:
		s.Guard.Fail()
	}
	return s
}

func TestWaitModeChoosesBusyOrStoppedResourceOnMerit(t *testing.T) {
	for _, state := range []runtime.SessionState{runtime.StateIdle, runtime.StateRunning, runtime.StateStopped, runtime.StateFailed} {
		plan := allocateWith(t, testConfig(), wraithIn(state))
		if plan.Action != qac.ActionContinue || plan.To != "wraith" {
			t.Fatalf("%s: plan = %s -> %s (%s)", state, plan.Action, plan.To, plan.Decision.Reason)
		}
	}
	// A resource with no session at all cannot be readied.
	if plan := allocateWith(t, testConfig(), nil); plan.Action != qac.ActionStop {
		t.Fatalf("missing session: plan = %s", plan.Action)
	}
}

func TestStopModeTreatsStoppedResourceAsUnavailable(t *testing.T) {
	cfg := testConfig()
	cfg.OnUnavailable = config.QACUnavailableStop
	plan := allocateWith(t, cfg, wraithIn(runtime.StateStopped))
	if plan.Action != qac.ActionStop {
		t.Fatalf("plan = %s -> %s", plan.Action, plan.To)
	}
	c, _ := New(cfg)
	if c.ReadiesUnavailable() {
		t.Fatal("stop mode readies unavailable resources")
	}
}

func TestRerouteModeMovesToNearestEligible(t *testing.T) {
	cfg := testConfig()
	cfg.OnUnavailable = config.QACUnavailableReroute
	plan := allocateWith(t, cfg, wraithIn(runtime.StateStopped))
	if plan.Action == qac.ActionStop || plan.To != "shade" {
		t.Fatalf("plan = %s -> %s (%s)", plan.Action, plan.To, plan.Decision.Reason)
	}
}

func TestWaitModeStillStopsOnBudgetAndCooldown(t *testing.T) {
	for name, budget := range map[string]string{"budget": "max_activations_per_run=1", "cooldown": "cooldown=\"10m\""} {
		cfg := loadQACConfig(t, "[qac.resources.wraith.budget]\n"+budget+"\n")
		for _, state := range []runtime.SessionState{runtime.StateIdle, runtime.StateRunning} {
			if plan := allocateWith(t, cfg, wraithIn(state)); plan.Action != qac.ActionStop {
				t.Fatalf("%s/%s: plan = %s -> %s", name, state, plan.Action, plan.To)
			}
		}
	}
}

// loadQACConfig loads a validated QAC configuration with extra TOML appended.
func loadQACConfig(t *testing.T, extra string) config.QACConfig {
	t.Helper()
	text := ""
	for _, agent := range []string{"wraith", "shade", "veil"} {
		text += "[agents." + agent + "]\nruntime=\"codex\"\nworking_dir=\".\"\n"
	}
	text += "[qac]\nenabled=true\nentry_resource=\"wraith\"\ndefault_importance=0.5\n[qac.policy]\ntype=\"threshold\"\nhierarchy=[\"wraith\",\"shade\",\"veil\"]\n"
	for _, r := range []string{"wraith\"\ncapability=0.3\ncost=0.1\nscarcity=0.05", "shade\"\ncapability=0.65\ncost=0.3\nscarcity=0.3", "veil\"\ncapability=0.95\ncost=0.8\nscarcity=0.95"} {
		text += "[qac.resources." + r[:strings.Index(r, "\"")] + "]\nagent=\"" + r + "\n"
	}
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte(text+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.QAC
}

func TestSnapshotStaysStrictInWaitMode(t *testing.T) {
	c, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot(map[string]runtime.Session{"wraith": wraithIn(runtime.StateRunning)}, time.Now()).Resources["wraith"].Available; got {
		t.Fatal("Snapshot reported a busy session as available")
	}
}
