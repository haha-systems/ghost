package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
)

func TestDefault(t *testing.T) {
	if got := config.Default().UI.Theme; got != "bloodwire" {
		t.Fatalf("default theme = %q, want bloodwire", got)
	}
	decisions := config.Default().Decisions
	if decisions.Enabled || decisions.CESAgent != "rules" || decisions.Fallback != "off" || decisions.Timeout() != 5*time.Second || decisions.MaxPerRun != 20 || decisions.MaxPerTask != 3 || decisions.FailureThreshold != 3 || decisions.AutoConsult {
		t.Fatalf("decision defaults = %#v", decisions)
	}
}

func TestDecisionConfigValidation(t *testing.T) {
	cfg, err := config.Decode(strings.NewReader("[decisions]\nenabled = true\nces_agent = \"rules\"\nfallback = \"off\"\ntimeout = \"2s\"\nmax_per_run = 7\nmax_per_task = 2\nfailure_threshold = 4\nauto_consult = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Decisions.Enabled || cfg.Decisions.Timeout() != 2*time.Second || cfg.Decisions.MaxPerRun != 7 || cfg.Decisions.MaxPerTask != 2 || cfg.Decisions.FailureThreshold != 4 || !cfg.Decisions.AutoConsult {
		t.Fatalf("decisions = %#v", cfg.Decisions)
	}
	_, err = config.Decode(strings.NewReader("[decisions]\nces_agent = \"jev\"\n"))
	if err == nil || !strings.Contains(err.Error(), "no Jev transport configured") {
		t.Fatalf("jev error = %v", err)
	}
}

func TestDecisionConfigRejectsInvalidBudgets(t *testing.T) {
	for _, text := range []string{
		"[decisions]\ntimeout = \"0s\"\n",
		"[decisions]\nmax_per_run = 0\n",
		"[decisions]\nmax_per_task = -1\n",
		"[decisions]\nfailure_threshold = 0\n",
		"[decisions]\nfallback = \"maybe\"\n",
	} {
		if _, err := config.Decode(strings.NewReader(text)); err == nil {
			t.Errorf("Decode(%q) succeeded", text)
		}
	}
}

func TestLoadValidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte("[ui]\ntheme = \"bloodwire\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Theme != "bloodwire" {
		t.Fatalf("theme = %q, want bloodwire", cfg.UI.Theme)
	}
}

func TestLoadPreservesAgentSourceOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.toml")
	text := "[agents.zulu]\nruntime = \"codex\"\n[agents.alpha]\nruntime = \"codex\"\n[agents.middle]\nruntime = \"codex\"\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zulu", "alpha", "middle"}
	if !reflect.DeepEqual(cfg.AgentOrder, want) {
		t.Fatalf("agent order = %v, want %v", cfg.AgentOrder, want)
	}
}

func TestLoadResolvesTracePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ghost.toml")
	if err := os.WriteFile(path, []byte("[trace]\npath=\"traces/ghost.jsonl\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Trace.Path != filepath.Join(dir, "traces", "ghost.jsonl") {
		t.Fatalf("path=%q", cfg.Trace.Path)
	}
}

func TestLoadMissingUsesDefaults(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, config.Default()) {
		t.Fatalf("missing file config = %#v, want defaults", cfg)
	}
}

func TestLoadMalformedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte("[ui\ntheme = \"broken\""), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("error = %v, want clear parse error", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte("[provider]\nname = \"forbidden\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("error = %v, want strict parse error", err)
	}
}

func TestLoadNamesUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte("[qac]\nunknown_setting = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "qac.unknown_setting") || !strings.Contains(err.Error(), "unknown setting") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadExtendedConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "prompts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"initial.md", "soul.md"} {
		if err := os.WriteFile(filepath.Join(dir, "prompts", name), []byte("prompt"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GHOST_TEST_KEY", "secret")
	path := filepath.Join(dir, "ghost.toml")
	text := "[global]\ninitial_prompt = \"prompts/initial.md\"\n[agents.veil]\nruntime = \"codex\"\nmodel = \"gpt-test\"\neffort = \"high\"\nworking_dir = \".\"\nmcp = true\nhooks = true\nsandboxed = true\nghost_mode = true\nsoul_prompt = \"prompts/soul.md\"\n[memory]\nenabled = true\ntrigger = \"turn_complete\"\n[memory.ghostdive]\nurl_base = \"http://localhost:8080\"\nauth = \"bearer_env\"\nkey = \"$GHOST_TEST_KEY\"\nspace_id = \"space-1\"\n[safety]\ntrust_all_hooks = true\n"
	text = strings.ReplaceAll(strings.ReplaceAll(text, "mcp = true", "mcp = false"), "\nhooks = true\n", "\nhooks = false\n")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Global.InitialPrompt != filepath.Join(dir, "prompts", "initial.md") {
		t.Fatalf("initial prompt = %q", cfg.Global.InitialPrompt)
	}
	agent := (*cfg.Agents)["veil"]
	if agent.Runtime != "codex" || agent.Model != "gpt-test" || agent.Effort != "high" || agent.WorkingDir != dir || agent.MCP || agent.Hooks || !agent.Sandboxed || !agent.GhostMode || agent.SoulPrompt != filepath.Join(dir, "prompts", "soul.md") {
		t.Fatalf("agent = %#v", agent)
	}
	if !cfg.Memory.Enabled || cfg.Memory.Trigger != "turn_complete" || cfg.Memory.Ghostdive.URLBase != "http://localhost:8080" || cfg.Memory.Ghostdive.Auth != "bearer_env" || cfg.Memory.Ghostdive.Key != "$GHOST_TEST_KEY" || cfg.Memory.Ghostdive.SpaceID != "space-1" {
		t.Fatalf("memory = %#v", cfg.Memory)
	}
	if !cfg.Safety.TrustAllHooks {
		t.Fatal("trust_all_hooks = false, want true")
	}
}

func TestUnsupportedCodexOptionsAreRejected(t *testing.T) {
	text := "[agents.veil]\nruntime=\"codex\"\nmcp=true\n"
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "mcp and hooks are not supported") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadDefersMissingMemoryEnvironmentKeyToRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.toml")
	text := "[memory]\nenabled = true\ntrigger = \"turn_complete\"\n[memory.ghostdive]\nurl_base = \"http://localhost:8080\"\nauth = \"bearer_env\"\nkey = \"$MISSING_GHOST_TEST_KEY\"\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("Load should defer the missing environment key to runtime: %v", err)
	}
}

func TestLoadRejectsInvalidExtendedValues(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"effort", "[agents.veil]\nruntime = \"codex\"\neffort = \"fast\"\nworking_dir = \".\"\n", "invalid effort"},
		{"steering submit", "[ui]\nsteering_submit = \"space\"\n", "invalid steering_submit"},
		{"prompt", "[global]\ninitial_prompt = \"missing.md\"\n", "initial_prompt"},
		{"memory URL", "[memory]\nenabled = true\ntrigger = \"turn_complete\"\n[memory.ghostdive]\nurl_base = \"ftp://localhost\"\nauth = \"none\"\n", "invalid url_base"},
		{"memory trigger", "[memory]\nenabled = true\ntrigger = \"later\"\n[memory.ghostdive]\nurl_base = \"http://localhost\"\nauth = \"none\"\n", "invalid trigger"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ghost.toml")
			if err := os.WriteFile(path, []byte(test.text), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := config.Load(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
