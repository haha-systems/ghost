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
	UI UIConfig `toml:"ui"`
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
	return cfg, nil
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
	return cfg, nil
}
