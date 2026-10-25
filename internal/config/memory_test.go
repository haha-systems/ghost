package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
)

const testSpaceID = "123e4567-e89b-12d3-a456-426614174000"

func TestMemoryDefaults(t *testing.T) {
	got := config.Default().Memory
	if got.MaxContextTokens != 800 || got.MaxContextRecords != 8 || got.RetrieveTimeout != "2s" || got.WriteTimeout != "5s" || got.WriteQueue != 64 || got.ShareGlobal {
		t.Fatalf("memory defaults = %#v", got)
	}
}

func TestLoadMemoryOptionsAndEmptyWorkspaceSpace(t *testing.T) {
	t.Setenv("GHOST_MEMORY_KEY", "")
	path := filepath.Join(t.TempDir(), "ghost.toml")
	text := "[memory]\nenabled=true\nshare_global=true\nmax_context_tokens=321\nmax_context_records=4\nretrieve_timeout=\"1200ms\"\nwrite_timeout=\"3s\"\nwrite_queue=9\n[memory.ghostdive]\nurl_base=\"http://localhost:8080\"\nauth=\"bearer_env\"\nkey=\"$GHOST_MEMORY_KEY\"\nspace_id=\"\"\nagent_spaces={ wraith=\"" + testSpaceID + "\" }\nglobal_space_id=\"123e4567-e89b-12d3-a456-426614174001\"\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load rejected an empty workspace space or unset key: %v", err)
	}
	if !cfg.Memory.Enabled || !cfg.Memory.ShareGlobal || cfg.Memory.MaxContextTokens != 321 || cfg.Memory.MaxContextRecords != 4 || cfg.Memory.RetrieveTimeout != "1200ms" || cfg.Memory.WriteTimeout != "3s" || cfg.Memory.WriteQueue != 9 {
		t.Fatalf("memory = %#v", cfg.Memory)
	}
	if cfg.Memory.Ghostdive.SpaceID != "" || cfg.Memory.Ghostdive.AgentSpaces["wraith"] != testSpaceID || cfg.Memory.Ghostdive.GlobalSpaceID == "" {
		t.Fatalf("ghostdive = %#v", cfg.Memory.Ghostdive)
	}
	if strings.Contains(cfg.Memory.Ghostdive.String(), "GHOST_MEMORY_KEY") || strings.Contains(cfg.Memory.Ghostdive.GoString(), "GHOST_MEMORY_KEY") {
		t.Fatal("config string form exposed the bearer environment reference")
	}
	cfg.Memory.Ghostdive.Key = "bearer-secret"
	if strings.Contains(cfg.Memory.Ghostdive.String(), "bearer-secret") || strings.Contains(cfg.Memory.Ghostdive.GoString(), "bearer-secret") {
		t.Fatal("config string form exposed the bearer value")
	}
	if strings.Contains(fmt.Sprintf("%+v", config.Config{Memory: cfg.Memory}), "bearer-secret") || strings.Contains(fmt.Sprintf("%#v", config.Config{Memory: cfg.Memory}), "bearer-secret") {
		t.Fatal("config dump exposed the bearer value")
	}
}

func TestDisabledMemoryIgnoresLegacyTrigger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.toml")
	if err := os.WriteFile(path, []byte("[memory]\ntrigger=\"old-value\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("disabled legacy trigger stopped config loading: %v", err)
	}
}

func TestLoadAllowsInvalidWorkspaceSpaceForRuntimeAndRejectsOtherSpaceShapes(t *testing.T) {
	for _, field := range []string{"agent_spaces", "global_space_id"} {
		t.Run(field, func(t *testing.T) {
			space := "not-a-uuid"
			value := "space_id=\"" + space + "\""
			switch field {
			case "agent_spaces":
				value = "agent_spaces={ wraith=\"" + space + "\" }"
			case "global_space_id":
				value = "global_space_id=\"" + space + "\""
			}
			text := "[memory]\nenabled=true\n[memory.ghostdive]\nurl_base=\"http://localhost:8080\"\nauth=\"none\"\n" + value + "\n"
			path := filepath.Join(t.TempDir(), "ghost.toml")
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := config.Load(path)
			if err == nil || !strings.Contains(err.Error(), "UUID") {
				t.Fatalf("Load error = %v, want UUID shape error", err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "ghost.toml")
	text := "[memory]\nenabled=true\n[memory.ghostdive]\nurl_base=\"http://localhost:8080\"\nauth=\"none\"\nspace_id=\"not-a-uuid\"\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("Load rejected workspace UUID that runtime must report unavailable: %v", err)
	}
}

func TestMemoryDurationsHaveUsableValues(t *testing.T) {
	cfg := config.Default().Memory
	retrieve, err := time.ParseDuration(cfg.RetrieveTimeout)
	if err != nil || retrieve != 2*time.Second {
		t.Fatalf("retrieve_timeout = %q, %v", cfg.RetrieveTimeout, err)
	}
}
