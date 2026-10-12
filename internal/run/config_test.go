package run

import "github.com/haha-systems/ghost/internal/config"

func appQACConfig() config.Config {
	agents := config.AgentSet{"wraith": {Runtime: "codex", WorkingDir: "."}, "shade": {Runtime: "codex", WorkingDir: "."}}
	cfg := config.Default()
	cfg.Agents = &agents
	cfg.QAC = config.QACConfig{Enabled: true, EntryResource: "wraith", DefaultImportance: .5, Policy: config.QACPolicyConfig{Type: "threshold", Hierarchy: []string{"wraith", "shade"}}, Resources: map[string]config.QACResourceConfig{"wraith": {Agent: "wraith", Capability: .3, Cost: .1, Scarcity: .05}, "shade": {Agent: "shade", Capability: .65, Cost: .3, Scarcity: .3}}}
	return cfg
}
