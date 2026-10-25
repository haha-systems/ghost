package run

import (
	"fmt"
	"os"
	"strings"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
)

func MakeSessionConfig(initialPrompt, agentID string, backend config.BackendConfig) (runtime.SessionConfig, error) {
	instructions, err := loadInstructions(initialPrompt, backend.SoulPrompt)

	if err != nil {
		return runtime.SessionConfig{}, err
	}

	// Unset resolves to workspace_write (Mark, 2026-10-25). The legacy
	// sandboxed key keeps its explicit meaning: false is full access.
	sandbox := runtime.SandboxEnabled
	if backend.Sandboxed != nil && !*backend.Sandboxed {
		sandbox = runtime.SandboxDisabled
	}
	switch backend.Sandbox {
	case "read_only":
		sandbox = runtime.SandboxReadOnly
	case "workspace_write":
		sandbox = runtime.SandboxEnabled
	case "full_access":
		sandbox = runtime.SandboxDisabled
	}
	approvals := runtime.ApprovalDefault
	if backend.GhostMode {
		approvals = runtime.ApprovalNever
	}

	return runtime.SessionConfig{
		AgentID:      agentID,
		WorkingDir:   backend.WorkingDir,
		Model:        backend.Model,
		Instructions: instructions,
		Effort:       backend.Effort,
		Sandbox:      sandbox,
		Approvals:    approvals,
	}, nil
}

func loadInstructions(paths ...string) (string, error) {
	parts := make([]string, 0, len(paths))

	for _, path := range paths {
		if path == "" {
			continue
		}

		content, err := os.ReadFile(path)

		if err != nil {
			return "", fmt.Errorf("read prompt %q: %w", path, err)
		}

		parts = append(parts, strings.TrimSpace(string(content)))
	}

	return strings.Join(parts, "\n\n"), nil
}
