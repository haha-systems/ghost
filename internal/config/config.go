// Package config loads the small set of settings used by Ghost's shell.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	UI     UIConfig     `toml:"ui"`
	Global GlobalConfig `toml:"global"`
	Agents *AgentSet    `toml:"agents"`
	Memory MemoryConfig `toml:"memory"`
	Safety SafetyConfig `toml:"safety"`
}

type GlobalConfig struct {
	InitialPrompt string `toml:"initial_prompt"`
}
type AgentSet map[string]BackendConfig
type BackendConfig struct {
	Runtime    string `toml:"runtime"`
	Model      string `toml:"model"`
	Effort     string `toml:"effort"`
	WorkingDir string `toml:"working_dir"`
	MCP        bool   `toml:"mcp"`
	Hooks      bool   `toml:"hooks"`
	Sandboxed  bool   `toml:"sandboxed"`
	GhostMode  bool   `toml:"ghost_mode"`
	SoulPrompt string `toml:"soul_prompt"`
}

type UIConfig struct {
	Theme string `toml:"theme"`
}

type MemoryConfig struct {
	Enabled   bool            `toml:"enabled"`
	Trigger   string          `toml:"trigger"`
	Ghostdive GhostdiveConfig `toml:"ghostdive"`
}

type GhostdiveConfig struct {
	URLBase string `toml:"url_base"`
	Auth    string `toml:"auth"`
	Key     string `toml:"key"`
	SpaceID string `toml:"space_id"`
}

type SafetyConfig struct {
	TrustAllHooks bool `toml:"trust_all_hooks"`
}

// Default returns the configuration used when no file is present.
func Default() Config {
	return Config{UI: UIConfig{Theme: "bloodwire"}}
}

// Load reads path using strict TOML decoding. An absent file is equivalent to
// no configuration, which keeps a first run zero-configuration.
func Load(path string) (Config, error) {
	if path == "" {
		return Default(), nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config %q: %w", path, err)
	}
	return decode(f, filepath.Dir(absolute), path)
}

func decode(r io.Reader, baseDir, label string) (Config, error) {
	cfg := Default()
	if err := toml.NewDecoder(r).DisallowUnknownFields().Decode(&cfg); err != nil {
		if label == "" {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
		return Config{}, fmt.Errorf("parse config %q: %w", label, err)
	}
	if err := validate(&cfg, baseDir); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validate(cfg *Config, baseDir string) error {
	var err error
	if cfg.Global.InitialPrompt, err = resolveFile(baseDir, cfg.Global.InitialPrompt); err != nil {
		return fmt.Errorf("global initial_prompt: %w", err)
	}
	if cfg.Agents != nil {
		for id, a := range *cfg.Agents {
			if id == "" || a.Runtime == "" {
				return fmt.Errorf("agent %q: runtime is required", id)
			}
			if !oneOf(a.Runtime, "codex", "claude", "openai-compat") {
				return fmt.Errorf("agent %q: invalid runtime %q", id, a.Runtime)
			}
			if a.Effort != "" && !oneOf(a.Effort, "low", "medium", "high", "xhigh", "max", "ultra") {
				return fmt.Errorf("agent %q: invalid effort %q", id, a.Effort)
			}
			if a.WorkingDir == "" {
				a.WorkingDir = "."
			}
			a.WorkingDir, err = resolveDir(baseDir, a.WorkingDir)
			if err != nil {
				return fmt.Errorf("agent %q: working_dir: %w", id, err)
			}
			a.SoulPrompt, err = resolveFile(baseDir, a.SoulPrompt)
			if err != nil {
				return fmt.Errorf("agent %q: soul_prompt: %w", id, err)
			}
			(*cfg.Agents)[id] = a
		}
	}
	if cfg.Memory.Enabled {
		if err := validateMemory(cfg.Memory); err != nil {
			return err
		}
	}
	return nil
}

func validateMemory(memory MemoryConfig) error {
	if !oneOf(memory.Trigger, "turn_complete", "top_and_tail", "top_and_tail_turn") {
		return fmt.Errorf("memory: invalid trigger %q", memory.Trigger)
	}
	u, err := url.Parse(memory.Ghostdive.URLBase)
	if err != nil || u.Host == "" || !oneOf(u.Scheme, "http", "https") || u.User != nil {
		return fmt.Errorf("memory ghostdive: invalid url_base %q", memory.Ghostdive.URLBase)
	}
	switch memory.Ghostdive.Auth {
	case "bearer_string":
		if memory.Ghostdive.Key == "" {
			return errors.New("memory ghostdive: key is required for bearer_string")
		}
	case "bearer_env":
		name := strings.TrimPrefix(memory.Ghostdive.Key, "$")
		if name == memory.Ghostdive.Key || name == "" {
			return errors.New("memory ghostdive: bearer_env key must name an environment variable")
		}
		if value, ok := os.LookupEnv(name); !ok || value == "" {
			return fmt.Errorf("memory ghostdive: environment variable %q is required", name)
		}
	case "none":
		if memory.Ghostdive.Key != "" {
			return errors.New("memory ghostdive: key must be empty when auth is none")
		}
	default:
		return fmt.Errorf("memory ghostdive: invalid auth %q", memory.Ghostdive.Auth)
	}
	return nil
}

func resolveDir(baseDir, value string) (string, error) {
	path := resolvePath(baseDir, value)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("is not a directory")
	}
	return path, nil
}

func resolveFile(baseDir, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	path := resolvePath(baseDir, value)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("is not a regular file")
	}
	return path, nil
}

func resolvePath(baseDir, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(baseDir, value)
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

// Decode is useful to callers and tests that already have a TOML stream.
// It applies defaults before decoding and rejects unknown fields.
func Decode(r io.Reader) (Config, error) {
	if r == nil {
		return Config{}, errors.New("decode config: nil reader")
	}
	baseDir, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("resolve current directory: %w", err)
	}
	return decode(r, baseDir, "")
}
