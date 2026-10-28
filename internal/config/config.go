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
	// AdminToken authorizes the operator endpoints (applications and API
	// keys). Like DatabaseURL, it must never be logged.
	AdminToken string
	// HTTPAddr is the host:port the API listens on.
	HTTPAddr string
	// LogLevel is the minimum level written to the log.
	LogLevel slog.Level
	// ShutdownTimeout bounds how long in-flight requests get to finish after
	// SIGINT or SIGTERM.
	ShutdownTimeout time.Duration
}

// minAdminTokenLen rejects tokens short enough to guess. 32 characters of a
// random string such as `openssl rand -hex 32` output is far beyond that.
const minAdminTokenLen = 32

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

	switch v, _ := lookup("CARTEIRO_ADMIN_TOKEN"); {
	case v == "":
		errs = append(errs, errors.New("CARTEIRO_ADMIN_TOKEN is required"))
	case len(v) < minAdminTokenLen:
		// The message gives the length, never the value.
		errs = append(errs, fmt.Errorf("CARTEIRO_ADMIN_TOKEN: must have at least %d characters, got %d", minAdminTokenLen, len(v)))
	default:
		cfg.AdminToken = v
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
