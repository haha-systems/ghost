package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
)

func TestMakeSessionConfigSandboxSelection(t *testing.T) {
	prompt := filepath.Join(t.TempDir(), "initial.md")
	if err := os.WriteFile(prompt, []byte("prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	cases := []struct {
		name    string
		backend config.BackendConfig
		want    runtime.SandboxMode
	}{
		// Unset resolves to workspace_write (Mark, 2026-10-25).
		{"unset is workspace write", config.BackendConfig{}, runtime.SandboxEnabled},
		// The legacy key keeps its explicit meaning.
		{"legacy sandboxed false is full access", config.BackendConfig{Sandboxed: &no}, runtime.SandboxDisabled},
		{"legacy sandboxed true is workspace write", config.BackendConfig{Sandboxed: &yes}, runtime.SandboxEnabled},
		{"read only", config.BackendConfig{Sandbox: "read_only"}, runtime.SandboxReadOnly},
		{"read only beats sandboxed true", config.BackendConfig{Sandboxed: &yes, Sandbox: "read_only"}, runtime.SandboxReadOnly},
		{"read only beats sandboxed false", config.BackendConfig{Sandboxed: &no, Sandbox: "read_only"}, runtime.SandboxReadOnly},
		{"workspace write", config.BackendConfig{Sandbox: "workspace_write"}, runtime.SandboxEnabled},
		{"workspace write beats sandboxed false", config.BackendConfig{Sandboxed: &no, Sandbox: "workspace_write"}, runtime.SandboxEnabled},
		{"explicit full access", config.BackendConfig{Sandbox: "full_access"}, runtime.SandboxDisabled},
	}
	for _, tc := range cases {
		got, err := MakeSessionConfig(prompt, "veil", tc.backend)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.Sandbox != tc.want {
			t.Fatalf("%s: sandbox = %v, want %v", tc.name, got.Sandbox, tc.want)
		}
	}
}
