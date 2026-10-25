package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/app"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/logging"
	"github.com/haha-systems/ghost/internal/serve"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e exitError) ExitCode() int { return e.code }

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		code := 1
		var coded exitError
		if errors.As(err, &coded) {
			code = coded.ExitCode()
		}
		if err.Error() != "" {
			fmt.Fprintln(os.Stderr, "ghost:", err)
		}
		os.Exit(code)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return serveCommand(args[1:], stdout, stderr)
		case "run":
			return runCommand(args[1:], stdout, stderr)
		}
	}
	flags := flag.NewFlagSet("ghost", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "ghost.toml", "path to the TOML configuration file")
	showVersion := flags.Bool("version", false, "print build metadata and exit")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Ghost — a restrained operator console for synthetic agents")
		_, _ = fmt.Fprintln(stderr, "\nUsage: ghost [options]")
		flags.PrintDefaults()
		_, _ = fmt.Fprintln(stderr, "\nKeyboard:")
		_, _ = fmt.Fprintln(stderr, "  Tab/Shift+Tab  cycle focus; the focused pane owns the arrow keys")
		_, _ = fmt.Fprintln(stderr, "  ↑/↓            scroll the focused pane, or select an agent in the roster")
		_, _ = fmt.Fprintln(stderr, "  PgUp/PgDn      page the focused pane; Home oldest, End resumes live follow")
		_, _ = fmt.Fprintln(stderr, "  Enter          open the selected agent; Ctrl+X interrupts a running turn")
		_, _ = fmt.Fprintln(stderr, "  Esc            clear a steering draft, else return to the dashboard")
		_, _ = fmt.Fprintln(stderr, "  ?              full key help; q quits; Ctrl+C always quits")
		_, _ = fmt.Fprintln(stderr, "\nThe mouse wheel scrolls whichever pane the pointer is over, without")
		_, _ = fmt.Fprintln(stderr, "moving keyboard focus. The bottom row always lists the actions available.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "ghost %s (commit %s, built %s)\n", version, commit, buildDate)
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
	previousLogger := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(previousLogger)
	defer func() {
		if err := closer.Close(); err != nil {
			logger.Warn("close diagnostic log", "error", err)
		}
	}()
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

func serveCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "ghost.toml", "path to the TOML configuration file")
	socketPath := flags.String("socket", "", "listen on this Unix socket")
	stdio := flags.Bool("stdio", false, "serve one client on stdin and stdout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return exitError{code: 2, err: err}
	}
	if *stdio == (*socketPath != "") {
		return exitError{code: 2, err: errors.New("choose one of --stdio or --socket")}
	}
	cfg, logger, closer, err := loadOperator(*configPath)
	if err != nil {
		return exitError{code: 2, err: err}
	}
	defer func() { _ = closer.Close() }()
	server, err := serve.New(cfg, logger)
	if err != nil {
		return exitError{code: 1, err: err}
	}
	defer func() { _ = server.Close() }()
	if *stdio {
		if err := server.Serve(context.Background(), os.Stdin, stdout); err != nil {
			return exitError{code: 1, err: err}
		}
		return nil
	}
	listener, err := serve.ListenSocket(*socketPath)
	if err != nil {
		return exitError{code: 1, err: err}
	}
	defer func() { _ = listener.Close() }()
	for {
		conn, err := serve.AcceptSameUser(listener)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			logger.Warn("reject connection", "error", err)
			continue
		}
		go func(conn net.Conn) {
			defer func() { _ = conn.Close() }()
			if err := server.Serve(context.Background(), conn, conn); err != nil && !errors.Is(err, io.EOF) {
				logger.Warn("connection closed", "error", err)
			}
		}(conn)
	}
}

func runCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "ghost.toml", "path to the TOML configuration file")
	task := flags.String("task", "", "task text")
	workspace := flags.String("workspace", "", "configured workspace root")
	jsonOut := flags.Bool("json", false, "write NDJSON events to stdout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return exitError{code: 2, err: err}
	}
	if !*jsonOut || *task == "" || *workspace == "" {
		return exitError{code: 2, err: errors.New("--task, --workspace, and --json are required")}
	}
	cfg, logger, closer, err := loadOperator(*configPath)
	if err != nil {
		return exitError{code: 2, err: err}
	}
	defer func() { _ = closer.Close() }()
	code, err := serve.RunOnce(cfg, logger, *task, *workspace, stdout)
	if err != nil && code == 0 {
		code = 1
	}
	if code != 0 {
		return exitError{code: code, err: err}
	}
	return nil
}

func loadOperator(configPath string) (config.Config, *slog.Logger, io.Closer, error) {
	logger, closer, err := logging.New(defaultLogPath())
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		_ = closer.Close()
		return config.Config{}, nil, nil, err
	}
	return cfg, logger, closer, nil
}

func defaultLogPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "ghost", "ghost.log")
	}
	return filepath.Join(".ghost", "ghost.log")
}
