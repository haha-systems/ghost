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
	cases := []struct {
		name    string
		backend config.BackendConfig
		want    runtime.SandboxMode
	}{
		// Existing behaviour is unchanged: an unset or false sandboxed is full access.
		{"unset keeps legacy full access", config.BackendConfig{}, runtime.SandboxDisabled},
		{"sandboxed true", config.BackendConfig{Sandboxed: true}, runtime.SandboxEnabled},
		{"read only", config.BackendConfig{Sandbox: "read_only"}, runtime.SandboxReadOnly},
		{"read only beats sandboxed", config.BackendConfig{Sandboxed: true, Sandbox: "read_only"}, runtime.SandboxReadOnly},
		{"workspace write", config.BackendConfig{Sandbox: "workspace_write"}, runtime.SandboxEnabled},
		{"full access", config.BackendConfig{Sandbox: "full_access"}, runtime.SandboxDisabled},
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
