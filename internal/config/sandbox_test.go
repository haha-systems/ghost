package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haha-systems/ghost/internal/config"
)

func loadSandboxConfig(t *testing.T, lines string) (config.Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "initial.md"), []byte("prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ghost.toml")
	text := "[global]\ninitial_prompt = \"initial.md\"\n[agents.veil]\nruntime = \"codex\"\nworking_dir = \".\"\n" + lines
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(path)
}

func TestSandboxOptionParsesAndValidates(t *testing.T) {
	for _, value := range []string{"read_only", "workspace_write", "full_access"} {
		cfg, err := loadSandboxConfig(t, "sandbox = \""+value+"\"\n")
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if got := (*cfg.Agents)["veil"].Sandbox; got != value {
			t.Fatalf("sandbox = %q, want %q", got, value)
		}
	}
	if _, err := loadSandboxConfig(t, "sandbox = \"yolo\"\n"); err == nil || !strings.Contains(err.Error(), "invalid sandbox") {
		t.Fatalf("invalid sandbox error = %v", err)
	}
	if _, err := loadSandboxConfig(t, "sandboxed = true\nsandbox = \"full_access\"\n"); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflict error = %v", err)
	}
	cfg, err := loadSandboxConfig(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := (*cfg.Agents)["veil"]; got.Sandbox != "" || got.Sandboxed {
		t.Fatalf("unset sandbox fields = %+v", got)
	}
}
