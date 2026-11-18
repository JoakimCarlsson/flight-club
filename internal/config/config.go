package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

// How long the server is given. Shutdown drains in-flight requests and
// ReadHeader bounds how long a client may take to send its headers. Neither
// is read from the environment: they are the shape of the process rather
// than of a deployment.
const (
	ShutdownTimeout   = 10 * time.Second
	ReadHeaderTimeout = 10 * time.Second
)

// Config is everything the process reads from its environment.
type Config struct {
	// Addr is the address the API listens on, from FLIGHT_CLUB_ADDR.
	Addr string
	// Log is how the process writes its own log lines.
	Log Log
}

// Log is how the process writes its own log lines.
type Log struct {
	// Level is the lowest level written, from FLIGHT_CLUB_LOG_LEVEL:
	// "debug", "info", "warn" or "error".
	Level string
	// Format is the line format, from FLIGHT_CLUB_LOG_FORMAT: "json" or
	// "text".
	Format string
}

// Defaults for every variable that is unset or empty.
const (
	defaultAddr     = ":7080"
	defaultLogLevel = "info"
	defaultLogFmt   = "json"
)

// Load reads the environment, first applying any .env in the working
// directory. A missing .env is not an error, and real environment variables
// win over the file. It returns an error when .env exists but cannot be
// read.
func Load() (Config, error) {
	if err := loadDotenv(); err != nil {
		return Config{}, err
	}
	return Config{
		Addr: env("FLIGHT_CLUB_ADDR", defaultAddr),
		Log: Log{
			Level:  env("FLIGHT_CLUB_LOG_LEVEL", defaultLogLevel),
			Format: env("FLIGHT_CLUB_LOG_FORMAT", defaultLogFmt),
		},
	}, nil
}

// loadDotenv applies .env when it exists. It returns an error when the file
// exists but cannot be read or parsed.
func loadDotenv() error {
	err := godotenv.Load()
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("config: reading .env: %w", err)
}

// env returns the variable key, or fallback when it is unset or empty.
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
