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
