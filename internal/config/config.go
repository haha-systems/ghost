// Package config loads the small set of settings used by Ghost's shell.
package config

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/haha-systems/qac/policy/threshold"
	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	UI     UIConfig     `toml:"ui"`
	Global GlobalConfig `toml:"global"`
	Agents *AgentSet    `toml:"agents"`
	Memory MemoryConfig `toml:"memory"`
	Safety SafetyConfig `toml:"safety"`
	Trace  TraceConfig  `toml:"trace"`
	QAC    QACConfig    `toml:"qac"`
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
	Theme          string `toml:"theme"`
	SteeringSubmit string `toml:"steering_submit"`
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
type TraceConfig struct {
	Path string `toml:"path"`
	// Verbose records raw backend payloads and full messages in the trace.
	Verbose bool `toml:"verbose"`
	// StallAfterText is how long a CES phase session may go without activity
	// before it is reported as stalled and failed. "0" disables it.
	StallAfterText string `toml:"stall_after"`
	stallAfter     time.Duration
}

// DefaultStallAfter applies when stall_after is not configured.
const DefaultStallAfter = 10 * time.Minute

func (c TraceConfig) StallAfter() time.Duration { return c.stallAfter }

type QACConfig struct {
	Enabled           bool                         `toml:"enabled"`
	EntryResource     string                       `toml:"entry_resource"`
	DefaultImportance float64                      `toml:"default_importance"`
	Policy            QACPolicyConfig              `toml:"policy"`
	Resources         map[string]QACResourceConfig `toml:"resources"`
}
type QACPolicyConfig struct {
	Type      string   `toml:"type"`
	Hierarchy []string `toml:"hierarchy"`
}
type QACResourceConfig struct {
	Agent      string          `toml:"agent"`
	Capability float64         `toml:"capability"`
	Cost       float64         `toml:"cost"`
	Scarcity   float64         `toml:"scarcity"`
	Budget     QACBudgetConfig `toml:"budget"`
}
type QACBudgetConfig struct {
	MaxActivationsPerRun *int   `toml:"max_activations_per_run"`
	CooldownText         string `toml:"cooldown"`
	cooldown             time.Duration
}

func (c QACBudgetConfig) Cooldown() time.Duration { return c.cooldown }

// Default returns the configuration used when no file is present.
func Default() Config {
	return Config{UI: UIConfig{Theme: "bloodwire", SteeringSubmit: "ctrl_enter"}}
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
			return Config{}, fmt.Errorf("parse config: %w", parseError(err))
		}
		return Config{}, fmt.Errorf("parse config %q: %w", label, parseError(err))
	}
	if err := validate(&cfg, baseDir); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseError(err error) error {
	var strict *toml.StrictMissingError
	if !errors.As(err, &strict) {
		return err
	}
	items := make([]string, 0, len(strict.Errors))
	for _, item := range strict.Errors {
		line, column := item.Position()
		items = append(items, fmt.Sprintf("unknown setting %q at %d:%d", strings.Join(item.Key(), "."), line, column))
	}
	return errors.New(strings.Join(items, "; "))
}

func validate(cfg *Config, baseDir string) error {
	if !oneOf(cfg.UI.SteeringSubmit, "enter", "ctrl_enter") {
		return fmt.Errorf("ui: invalid steering_submit %q", cfg.UI.SteeringSubmit)
	}
	var err error
	if cfg.Global.InitialPrompt, err = resolveFile(baseDir, cfg.Global.InitialPrompt); err != nil {
		return fmt.Errorf("global initial_prompt: %w", err)
	}
	if cfg.Agents != nil {
		for id, a := range *cfg.Agents {
			if id == "" || a.Runtime == "" {
				return fmt.Errorf("agent %q: runtime is required", id)
			}
			// Only runtimes Ghost can actually start are accepted. Admitting a
			// planned runtime here defers the failure to startup, where it
			// surfaces as a red event on a console that looks otherwise healthy.
			if !oneOf(a.Runtime, "codex") {
				if oneOf(a.Runtime, "claude", "openai-compat") {
					return fmt.Errorf("agent %q: runtime %q is not implemented yet; use \"codex\"", id, a.Runtime)
				}
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
	if cfg.QAC.Enabled {
		if err := validateQAC(cfg); err != nil {
			return err
		}
	}
	if cfg.Trace.Path != "" {
		cfg.Trace.Path = resolvePath(baseDir, cfg.Trace.Path)
	}
	cfg.Trace.stallAfter = DefaultStallAfter
	if cfg.Trace.StallAfterText != "" {
		d, err := time.ParseDuration(cfg.Trace.StallAfterText)
		if err != nil || d < 0 {
			return fmt.Errorf("trace: stall_after %q is invalid", cfg.Trace.StallAfterText)
		}
		cfg.Trace.stallAfter = d
	}
	return nil
}

func validateQAC(cfg *Config) error {
	q := &cfg.QAC
	if cfg.Agents == nil {
		return errors.New("qac: agents are required")
	}
	if !normalized(q.DefaultImportance) {
		return errors.New("qac: default_importance must be in [0,1]")
	}
	if q.Policy.Type != "threshold" {
		return fmt.Errorf("qac: invalid policy type %q", q.Policy.Type)
	}
	if len(q.Policy.Hierarchy) == 0 {
		return errors.New("qac: hierarchy is required")
	}
	seen := map[string]bool{}
	for _, id := range q.Policy.Hierarchy {
		if id == "" || seen[id] {
			return fmt.Errorf("qac: invalid hierarchy resource %q", id)
		}
		seen[id] = true
		if _, ok := q.Resources[id]; !ok {
			return fmt.Errorf("qac: hierarchy resource %q is not configured", id)
		}
	}
	if _, ok := q.Resources[q.EntryResource]; !ok {
		return fmt.Errorf("qac: entry_resource %q is not configured", q.EntryResource)
	}
	agents := map[string]string{}
	for id, r := range q.Resources {
		if r.Agent == "" {
			return fmt.Errorf("qac: resource %q agent is required", id)
		}
		if _, ok := (*cfg.Agents)[r.Agent]; !ok {
			return fmt.Errorf("qac: resource %q maps unknown agent %q", id, r.Agent)
		}
		if prior, ok := agents[r.Agent]; ok {
			return fmt.Errorf("qac: resource %q and %q map the same agent %q", prior, id, r.Agent)
		}
		agents[r.Agent] = id
		if !normalized(r.Capability) || !normalized(r.Cost) || !normalized(r.Scarcity) {
			return fmt.Errorf("qac: resource %q capability, cost, and scarcity must be in [0,1]", id)
		}
		if r.Budget.MaxActivationsPerRun != nil && *r.Budget.MaxActivationsPerRun < 0 {
			return fmt.Errorf("qac: resource %q max_activations_per_run must be non-negative", id)
		}
		if r.Budget.CooldownText != "" {
			d, e := time.ParseDuration(r.Budget.CooldownText)
			if e != nil || d < 0 {
				return fmt.Errorf("qac: resource %q cooldown is invalid", id)
			}
			r.Budget.cooldown = d
			q.Resources[id] = r
		}
	}
	if _, err := threshold.New(threshold.Config{Hierarchy: q.Policy.Hierarchy}); err != nil {
		return fmt.Errorf("qac: construct threshold policy: %w", err)
	}
	return nil
}

func normalized(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }

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
	return slices.Contains(values, value)
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
