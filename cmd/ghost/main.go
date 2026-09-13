package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/app"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/logging"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ghost:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ghost", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "ghost.toml", "path to the TOML configuration file")
	showVersion := flags.Bool("version", false, "print build metadata and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Ghost — a restrained operator console for synthetic agents")
		fmt.Fprintln(stderr, "\nUsage: ghost [options]")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "\nKeyboard:")
		fmt.Fprintln(stderr, "  Tab/Shift+Tab  cycle focus; the focused pane owns the arrow keys")
		fmt.Fprintln(stderr, "  ↑/↓            scroll the focused pane, or select an agent in the roster")
		fmt.Fprintln(stderr, "  PgUp/PgDn      page the focused pane; Home oldest, End resumes live follow")
		fmt.Fprintln(stderr, "  Enter          open the selected agent; Ctrl+X interrupts a running turn")
		fmt.Fprintln(stderr, "  Esc            clear a steering draft, else return to the dashboard")
		fmt.Fprintln(stderr, "  ?              full key help; q quits; Ctrl+C always quits")
		fmt.Fprintln(stderr, "\nThe mouse wheel scrolls whichever pane the pointer is over, without")
		fmt.Fprintln(stderr, "moving keyboard focus. The bottom row always lists the actions available.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Fprintf(stdout, "ghost %s (commit %s, built %s)\n", version, commit, buildDate)
		return nil
	}

	logPath := defaultLogPath()
	logger, closer, err := logging.New(logPath)
	if err != nil {
		fallback := filepath.Join(".ghost", "ghost.log")
		logger, closer, err = logging.New(fallback)
		if err != nil {
			return fmt.Errorf("configure logging: %w", err)
		}
		logPath = fallback
	}
	defer closer.Close()
	logger.Info("loading config", "path", *configPath)
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("config loading failed", "error", err)
		return err
	}
	logger.Info("ghost starting", "version", version, "config", *configPath, "log", logPath)

	program := tea.NewProgram(app.New(cfg, logger))
	if _, err := program.Run(); err != nil {
		logger.Error("ghost stopped with error", "error", err)
		return err
	}
	logger.Info("ghost stopped")
	return nil
}

func defaultLogPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "ghost", "ghost.log")
	}
	return filepath.Join(".ghost", "ghost.log")
}
