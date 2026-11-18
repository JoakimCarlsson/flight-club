// Command api serves the flight-club API.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joakimcarlsson/flight-club/internal/config"
	"github.com/joakimcarlsson/flight-club/internal/httpx"
)

// version is the reported build version; override with -ldflags.
var version = "dev"

// main runs the API and exits with status 1 if it fails.
func main() {
	if err := run(); err != nil {
		slog.Error("api failed", "error", err)
		os.Exit(1)
	}
}

// run loads the configuration and serves HTTP until SIGINT or SIGTERM. It
// returns an error when the configuration is invalid, the address cannot be
// bound or the shutdown does not finish in time.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger, err := newLogger(cfg.Log)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	srv := httpx.New(httpx.Config{Addr: cfg.Addr, Version: version})
	return srv.Start(ctx)
}

// newLogger builds the process logger writing to stdout at the level and in
// the format cfg names. It returns an error for a level or format it does
// not know.
func newLogger(cfg config.Log) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", cfg.Level, err)
	}
	opts := &slog.HandlerOptions{Level: level}
	switch cfg.Format {
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stdout, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(os.Stdout, opts)), nil
	default:
		return nil, fmt.Errorf("log format %q: want json or text", cfg.Format)
	}
}
