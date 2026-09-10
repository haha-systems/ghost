// Package config loads the small set of settings used by Ghost's shell.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Config is the complete Phase 0 configuration. Keep this deliberately small:
// provider and runtime settings do not belong in the application shell yet.
type Config struct {
	UI     UIConfig  `toml:"ui"`
	Agents *AgentSet `toml:"agents"`
}
type AgentSet map[string]BackendConfig
type BackendConfig struct {
	Runtime    string `toml:"runtime"`
	WorkingDir string `toml:"working_dir"`
	Model      string `toml:"model"`
}

type UIConfig struct {
	Theme string `toml:"theme"`
}

// Default returns the configuration used when no file is present.
func Default() Config {
	return Config{UI: UIConfig{Theme: "bloodwire"}}
}

// Load reads path using strict TOML decoding. An absent file is equivalent to
// no configuration, which keeps a first run zero-configuration.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()

	dec := toml.NewDecoder(f).DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validate(cfg Config) error {
	if cfg.Agents == nil {
		return nil
	}
	for id, a := range *cfg.Agents {
		if id == "" || a.Runtime == "" {
			return fmt.Errorf("agent %q: runtime is required", id)
		}
		if a.Runtime != "codex" {
			return fmt.Errorf("agent %q: unsupported runtime %q", id, a.Runtime)
		}
		workingDir := a.WorkingDir
		if workingDir == "" {
			workingDir = "."
		}
		if workingDir != "" {
			info, err := os.Stat(workingDir)
			if err != nil {
				return fmt.Errorf("agent %q: working_dir: %w", id, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("agent %q: working_dir is not a directory", id)
			}
		}
	}
	return nil
}

// Decode is useful to callers and tests that already have a TOML stream.
// It applies defaults before decoding and rejects unknown fields.
func Decode(r io.Reader) (Config, error) {
	if r == nil {
		return Config{}, errors.New("decode config: nil reader")
	}
	cfg := Default()
	if err := toml.NewDecoder(r).DisallowUnknownFields().Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
