package run

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
)

// ConfiguredWorkDir returns a stable log root for one controller built from a
// multi-agent configuration. Configured agent workdirs are expected to name
// one workspace; when they differ, the first agent by name selects the root.
func ConfiguredWorkDir(cfg config.Config) string {
	if cfg.Agents == nil || len(*cfg.Agents) == 0 {
		return ""
	}
	names := make([]string, 0, len(*cfg.Agents))
	for name := range *cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.TrimSpace((*cfg.Agents)[names[0]].WorkingDir)
}

func openRunEventLog(workdir string) (*core.EventLog, string, error) {
	if workdir == "" {
		return nil, "", fmt.Errorf("work directory is required")
	}
	dir := filepath.Join(workdir, ".ghost", "events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create event log directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("set event log directory permissions: %w", err)
	}
	startID, err := core.NewID("session")
	if err != nil {
		return nil, "", fmt.Errorf("make event log session ID: %w", err)
	}
	path := filepath.Join(dir, startID+".jsonl")
	log, err := core.Open(path, true)
	if err != nil {
		return nil, "", fmt.Errorf("open event log: %w", err)
	}
	return log, path, nil
}
