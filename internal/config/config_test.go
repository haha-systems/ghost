package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/config"
)

func TestDefault(t *testing.T) {
	if got := config.Default().UI.Theme; got != "bloodwire" {
		t.Fatalf("default theme = %q, want bloodwire", got)
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

func TestLoadMissingUsesDefaults(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg != config.Default() {
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
	if agent.Runtime != "codex" || agent.Model != "gpt-test" || agent.Effort != "high" || agent.WorkingDir != dir || !agent.MCP || !agent.Hooks || !agent.Sandboxed || !agent.GhostMode || agent.SoulPrompt != filepath.Join(dir, "prompts", "soul.md") {
		t.Fatalf("agent = %#v", agent)
	}
	if !cfg.Memory.Enabled || cfg.Memory.Trigger != "turn_complete" || cfg.Memory.Ghostdive.URLBase != "http://localhost:8080" || cfg.Memory.Ghostdive.Auth != "bearer_env" || cfg.Memory.Ghostdive.Key != "$GHOST_TEST_KEY" || cfg.Memory.Ghostdive.SpaceID != "space-1" {
		t.Fatalf("memory = %#v", cfg.Memory)
	}
	if !cfg.Safety.TrustAllHooks {
		t.Fatal("trust_all_hooks = false, want true")
	}
}

func TestLoadRejectsInvalidMemoryAuthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.toml")
	text := "[memory]\nenabled = true\ntrigger = \"turn_complete\"\n[memory.ghostdive]\nurl_base = \"http://localhost:8080\"\nauth = \"bearer_env\"\nkey = \"$MISSING_GHOST_TEST_KEY\"\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "environment variable") {
		t.Fatalf("error = %v, want missing environment variable", err)
	}
}

func TestLoadRejectsInvalidExtendedValues(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"effort", "[agents.veil]\nruntime = \"codex\"\neffort = \"fast\"\nworking_dir = \".\"\n", "invalid effort"},
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
