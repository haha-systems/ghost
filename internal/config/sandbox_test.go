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
	if got := (*cfg.Agents)["veil"]; got.Sandbox != "" || got.Sandboxed != nil {
		t.Fatalf("unset sandbox fields = %+v", got)
	}
}

func TestLegacySandboxedKeyKeepsExplicitValue(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{{"sandboxed = false\n", false}, {"sandboxed = true\n", true}} {
		cfg, err := loadSandboxConfig(t, tc.line)
		if err != nil {
			t.Fatal(err)
		}
		got := (*cfg.Agents)["veil"].Sandboxed
		if got == nil || *got != tc.want {
			t.Fatalf("%q: sandboxed = %v, want explicit %v", tc.line, got, tc.want)
		}
	}
	if _, err := loadSandboxConfig(t, "sandboxed = false\nsandbox = \"full_access\"\n"); err != nil {
		t.Fatalf("explicit full access with sandboxed = false: %v", err)
	}
}
