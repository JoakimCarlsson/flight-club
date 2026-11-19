// Command api serves the flight-club API.
//
// Run as "api aeroplan-login" it instead signs the Aeroplan member of
// AEROPLAN_EMAIL and AEROPLAN_PASSWORD in once, asking at the terminal for
// the code aircanada.com emails, and saves the session the Aeroplan
// program searches with to AEROPLAN_SESSION_FILE. Set
// AEROPLAN_LOGIN_HEADFUL=true to watch the browser while it does.
package main

import (
	"bufio"
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
	"github.com/joakimcarlsson/flight-club/internal/award/aeroplan"
	"github.com/joakimcarlsson/flight-club/internal/award/airchina"
	"github.com/joakimcarlsson/flight-club/internal/award/alaska"
	"github.com/joakimcarlsson/flight-club/internal/award/american"
	"github.com/joakimcarlsson/flight-club/internal/award/ana"
	"github.com/joakimcarlsson/flight-club/internal/award/azul"
	"github.com/joakimcarlsson/flight-club/internal/award/cathay"
	"github.com/joakimcarlsson/flight-club/internal/award/copa"
	"github.com/joakimcarlsson/flight-club/internal/award/delta"
	"github.com/joakimcarlsson/flight-club/internal/award/etihad"
	"github.com/joakimcarlsson/flight-club/internal/award/frontier"
	"github.com/joakimcarlsson/flight-club/internal/award/jetblue"
	"github.com/joakimcarlsson/flight-club/internal/award/korean"
	"github.com/joakimcarlsson/flight-club/internal/award/lufthansa"
	"github.com/joakimcarlsson/flight-club/internal/award/qantas"
	"github.com/joakimcarlsson/flight-club/internal/award/sas"
	"github.com/joakimcarlsson/flight-club/internal/award/southwest"
	"github.com/joakimcarlsson/flight-club/internal/award/turkish"
	"github.com/joakimcarlsson/flight-club/internal/award/velocity"
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

// main runs the API, or the Aeroplan sign-in when its first argument is
// aeroplan-login, and exits with status 1 if it fails.
func main() {
	runner, name := run, "api"
	if len(os.Args) > 1 && os.Args[1] == "aeroplan-login" {
		runner, name = aeroplanLogin, "aeroplan-login"
	}
	if err := runner(); err != nil {
		slog.Error(name+" failed", "error", err)
		os.Exit(1)
	}
}

// aeroplanLogin signs the configured Aeroplan member in through a browser,
// reading the emailed verification code from standard input when the site
// asks for one, and saves the session. It returns an error when no member
// is configured, the browser cannot start or the sign-in fails.
func aeroplanLogin() error {
	if _, err := config.Load(); err != nil {
		return err
	}
	account, ok := aeroplan.AccountFromEnv()
	if !ok {
		return errors.New("AEROPLAN_EMAIL and AEROPLAN_PASSWORD must be set")
	}
	b, err := browser.New(browser.Options{
		Headless:   os.Getenv("AEROPLAN_LOGIN_HEADFUL") != "true",
		ChromePath: os.Getenv("FLIGHT_CLUB_CHROME_PATH"),
		NoSandbox:  os.Getenv("FLIGHT_CLUB_NO_SANDBOX") == "true",
	})
	if err != nil {
		return err
	}
	defer func() { _ = b.Close() }()
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	fmt.Println("signing in to Aeroplan as", account.Email)
	path, err := aeroplan.New(b, account).SignIn(ctx, readCode)
	if err != nil {
		return err
	}
	fmt.Println("signed in; session saved to", path)
	return nil
}

// readCode asks at the terminal for the verification code aircanada.com
// emailed and returns the line typed. It returns an error when standard
// input ends before a line is read.
func readCode(context.Context) (string, error) {
	fmt.Print("verification code from the email: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
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
		{
			Program:     ana.New(newClient()),
			Concurrency: 2,
			MinInterval: 500 * time.Millisecond,
		},
		{
			Program:     cathay.New(newClient()),
			Concurrency: 2,
			MinInterval: 500 * time.Millisecond,
		},
		{
			Program:     lufthansa.New(newClient()),
			Concurrency: 1,
			MinInterval: 2 * time.Second,
		},
		{
			Program:     airchina.New(newClient()),
			Concurrency: 1,
			MinInterval: 2 * time.Second,
		},
	}
	b, err := browser.New(browser.Options{
		Headless:    true,
		Concurrency: 4,
		ChromePath:  os.Getenv("FLIGHT_CLUB_CHROME_PATH"),
		NoSandbox:   os.Getenv("FLIGHT_CLUB_NO_SANDBOX") == "true",
	})
	if err != nil {
		slog.WarnContext(ctx, "no browser, skipping browser programs",
			"error", err)
	} else {
		defer func() { _ = b.Close() }()
		programs = append(programs,
			browserProgram(jetblue.New(b), 2, 2*time.Second),
			browserProgram(delta.New(b), 1, 5*time.Second),
			browserProgram(southwest.New(b), 1, 10*time.Second),
			browserProgram(turkish.New(b), 1, 30*time.Second),
			browserProgram(etihad.New(b), 1, 10*time.Second),
			browserProgram(qantas.New(b), 1, 5*time.Second),
			browserProgram(velocity.New(b), 1, 10*time.Second),
			browserProgram(korean.New(b), 1, 5*time.Second),
			browserProgram(copa.New(b), 1, 10*time.Second),
			browserProgram(azul.New(b), 1, 10*time.Second),
			browserProgram(frontier.New(b), 1, 10*time.Second),
			browserProgram(american.New(b), 1, 30*time.Second),
		)
		if account, ok := aeroplan.AccountFromEnv(); ok {
			programs = append(programs, browserProgram(
				aeroplan.New(b, account), 1, 30*time.Second))
		} else {
			slog.InfoContext(ctx, "no Aeroplan account, skipping aeroplan")
		}
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

// browserProgram paces p, a program that drives the shared browser, to at
// most n requests at once and one start per interval.
func browserProgram(
	p award.Program,
	n int,
	interval time.Duration,
) award.Paced {
	return award.Paced{Program: p, Concurrency: n, MinInterval: interval}
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
