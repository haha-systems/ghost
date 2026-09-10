package config_test

import (
	"github.com/haha-systems/ghost/internal/config"
	"strings"
	"testing"
)

func TestDecodeConfiguredCodexAgent(t *testing.T) {
	cfg, err := config.Decode(strings.NewReader("[agents.veil]\nruntime=\"codex\"\nworking_dir=\".\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents == nil || (*cfg.Agents)["veil"].Runtime != "codex" {
		t.Fatalf("agents=%#v", cfg.Agents)
	}
}
