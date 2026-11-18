// Command api serves the flight-club API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joakimcarlsson/ai/llm"
	llmopenai "github.com/joakimcarlsson/ai/llm/openai"
	llmopenrouter "github.com/joakimcarlsson/ai/llm/openrouter"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/award/alaska"
	"github.com/joakimcarlsson/flight-club/internal/award/jetblue"
	"github.com/joakimcarlsson/flight-club/internal/award/sas"
	"github.com/joakimcarlsson/flight-club/internal/award/virgin"
	"github.com/joakimcarlsson/flight-club/internal/browser"
	"github.com/joakimcarlsson/flight-club/internal/chat"
	"github.com/joakimcarlsson/flight-club/internal/config"
	"github.com/joakimcarlsson/flight-club/internal/httpx"
)

// programTimeout is the longest one loyalty program is waited on for one
// search.
const programTimeout = 30 * time.Second

// newClient returns an HTTP client for one loyalty program, bounded by
// programTimeout.
func newClient() *http.Client {
	return &http.Client{Timeout: programTimeout}
}

// errLLMNotConfigured is returned by newLLM when no API key is set.
var errLLMNotConfigured = errors.New("OPENROUTER_API_KEY is not set")

// newLLM builds the OpenRouter client straight from the llm packages. It
// returns errLLMNotConfigured when cfg has no API key, and an error when it
// has no model or the model is not in the library's OpenRouter catalogue.
func newLLM(cfg config.OpenRouter) (llm.LLM, error) {
	if cfg.APIKey == "" {
		return nil, errLLMNotConfigured
	}
	if cfg.Model == "" {
		return nil, errors.New("OPENROUTER_MODEL is not set")
	}
	for _, m := range llmopenrouter.Models {
		if m.APIModel == cfg.Model {
			return llmopenrouter.NewLLM(
				llmopenai.WithAPIKey(cfg.APIKey),
				llmopenai.WithModel(m),
				llmopenai.WithTimeout(2*time.Minute),
			), nil
		}
	}
	return nil, fmt.Errorf("OPENROUTER_MODEL %q is not in the OpenRouter catalogue", cfg.Model)
}

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

	programs := []award.Paced{
		{
			Program:     alaska.New(newClient()),
			Concurrency: 2,
			MinInterval: 500 * time.Millisecond,
		},
		{
			Program:     sas.New(newClient()),
			Concurrency: 2,
			MinInterval: time.Second,
		},
		{
			Program:     virgin.New(newClient()),
			Concurrency: 2,
			MinInterval: time.Second,
		},
	}
	b, err := browser.New(browser.Options{Headless: true, Concurrency: 2})
	if err != nil {
		slog.WarnContext(ctx, "no browser, skipping jetblue", "error", err)
	} else {
		defer func() { _ = b.Close() }()
		programs = append(programs, award.Paced{
			Program:     jetblue.New(b),
			Concurrency: 2,
			MinInterval: 2 * time.Second,
		})
	}
	awards := award.NewSearcher(programs...)
	var assistant *chat.Assistant
	model, err := newLLM(cfg.OpenRouter)
	switch {
	case errors.Is(err, errLLMNotConfigured):
		slog.WarnContext(ctx, "no LLM client", "reason", err)
	case err != nil:
		return err
	default:
		assistant = chat.New(model, awards)
	}
	srv := httpx.New(
		httpx.Config{Addr: cfg.Addr, Version: version},
		httpx.Deps{Awards: awards, Chat: assistant},
	)
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
