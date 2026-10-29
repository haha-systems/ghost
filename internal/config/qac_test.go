package config_test

import (
	"github.com/haha-systems/ghost/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func qacConfig() string {
	return "[agents.wraith]\nruntime=\"codex\"\nworking_dir=\".\"\n[agents.shade]\nruntime=\"codex\"\nworking_dir=\".\"\n[agents.veil]\nruntime=\"codex\"\nworking_dir=\".\"\n[qac]\nenabled=true\nentry_resource=\"wraith\"\ndefault_importance=0.5\n[qac.policy]\ntype=\"threshold\"\nhierarchy=[\"wraith\",\"shade\",\"veil\"]\n[qac.resources.wraith]\nagent=\"wraith\"\ncapability=0.3\ncost=0.1\nscarcity=0.05\n[qac.resources.shade]\nagent=\"shade\"\ncapability=0.65\ncost=0.3\nscarcity=0.3\n[qac.resources.veil]\nagent=\"veil\"\ncapability=0.95\ncost=0.8\nscarcity=0.95\n[qac.resources.veil.budget]\nmax_activations_per_run=3\ncooldown=\"10m\"\n"
}
func loadQAC(t *testing.T, text string) (config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(path)
}
func TestLoadQACConfig(t *testing.T) {
	cfg, err := loadQAC(t, qacConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.QAC.Enabled || cfg.QAC.EntryResource != "wraith" || len(cfg.QAC.Policy.Hierarchy) != 3 {
		t.Fatalf("qac=%#v", cfg.QAC)
	}
	if got := cfg.QAC.Resources["veil"].Budget.Cooldown(); got.String() != "10m0s" {
		t.Fatalf("cooldown=%s", got)
	}
}
func TestInvalidQACConfigRejected(t *testing.T) {
	for _, tt := range []struct{ name, old, new, want string }{{"entry", "entry_resource=\"wraith\"", "entry_resource=\"missing\"", "entry_resource"}, {"entry hierarchy", "hierarchy=[\"wraith\",\"shade\",\"veil\"]", "hierarchy=[\"shade\",\"veil\"]", "entry_resource"}, {"agent", "agent=\"shade\"\ncapability=0.65", "agent=\"wraith\"\ncapability=0.65", "same agent"}, {"value", "cost=0.1", "cost=1.1", "cost"}, {"limit", "max_activations_per_run=3", "max_activations_per_run=-1", "max_activations"}, {"cooldown", "cooldown=\"10m\"", "cooldown=\"later\"", "cooldown"}, {"policy", "type=\"threshold\"", "type=\"other\"", "policy"}} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadQAC(t, strings.Replace(qacConfig(), tt.old, tt.new, 1))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want %q", err, tt.want)
			}
		})
	}
}

func TestQACOnUnavailableDefaultsAndValues(t *testing.T) {
	cfg, err := loadQAC(t, qacConfig())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QAC.OnUnavailable != config.QACUnavailableWait || cfg.QAC.UnavailableTimeout() != config.DefaultUnavailableTimeout {
		t.Fatalf("defaults = %q %s", cfg.QAC.OnUnavailable, cfg.QAC.UnavailableTimeout())
	}
	for _, mode := range []string{"wait", "stop", "reroute"} {
		text := strings.Replace(qacConfig(), "default_importance=0.5\n", "default_importance=0.5\non_unavailable=\""+mode+"\"\nunavailable_timeout=\"90s\"\n", 1)
		cfg, err := loadQAC(t, text)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.QAC.OnUnavailable != mode || cfg.QAC.UnavailableTimeout().String() != "1m30s" {
			t.Fatalf("%s: got %q %s", mode, cfg.QAC.OnUnavailable, cfg.QAC.UnavailableTimeout())
		}
	}
	for _, tt := range []struct{ line, want string }{{"on_unavailable=\"retry\"", "on_unavailable"}, {"unavailable_timeout=\"soon\"", "unavailable_timeout"}, {"unavailable_timeout=\"0s\"", "unavailable_timeout"}} {
		text := strings.Replace(qacConfig(), "default_importance=0.5\n", "default_importance=0.5\n"+tt.line+"\n", 1)
		if _, err := loadQAC(t, text); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s: error = %v", tt.line, err)
		}
	}
}
