// Package config loads Carteiro's configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// Config holds every setting Carteiro reads at startup.
type Config struct {
	// DatabaseURL is a PostgreSQL connection string. It contains a password,
	// so it must never be logged.
	DatabaseURL string
	// HTTPAddr is the host:port the API listens on.
	HTTPAddr string
	// LogLevel is the minimum level written to the log.
	LogLevel slog.Level
	// ShutdownTimeout bounds how long in-flight requests get to finish after
	// SIGINT or SIGTERM.
	ShutdownTimeout time.Duration
}

// Load reads the configuration through lookup, which is os.LookupEnv in
// production and a map in tests. It reports every invalid variable at once,
// so a misconfigured deploy is fixed in one round instead of one per variable.
func Load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		HTTPAddr:        ":8080",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 15 * time.Second,
	}
	var errs []error

	if v, ok := lookup("CARTEIRO_DATABASE_URL"); ok && v != "" {
		cfg.DatabaseURL = v
	} else {
		errs = append(errs, errors.New("CARTEIRO_DATABASE_URL is required"))
	}

	if v, ok := lookup("CARTEIRO_HTTP_ADDR"); ok {
		if _, _, err := net.SplitHostPort(v); err != nil {
			errs = append(errs, fmt.Errorf("CARTEIRO_HTTP_ADDR: %w", err))
		} else {
			cfg.HTTPAddr = v
		}
	}

	if v, ok := lookup("CARTEIRO_LOG_LEVEL"); ok {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("CARTEIRO_LOG_LEVEL: %w", err))
		}
	}

	if v, ok := lookup("CARTEIRO_SHUTDOWN_TIMEOUT"); ok {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("CARTEIRO_SHUTDOWN_TIMEOUT: %w", err))
		case d <= 0:
			errs = append(errs, fmt.Errorf("CARTEIRO_SHUTDOWN_TIMEOUT: must be positive, got %s", d))
		default:
			cfg.ShutdownTimeout = d
		}
	}

	return cfg, errors.Join(errs...)
}
