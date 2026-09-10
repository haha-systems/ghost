// Package logging provides diagnostic logging that does not write to the TUI.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// New creates a JSON structured logger and its owned file. The caller must
// close the returned file when the application exits.
func New(path string) (*slog.Logger, io.Closer, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("open log: empty path")
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, fmt.Errorf("create log directory %q: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log %q: %w", path, err)
	}
	return slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})), f, nil
}
