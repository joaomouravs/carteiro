// Package config loads Carteiro's configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
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
	// EncryptionKey encrypts endpoint secrets at rest (AES-256, 32 bytes).
	// Losing it makes every stored secret unreadable; leaking it together
	// with a database dump leaks them all.
	EncryptionKey []byte
	// AllowHTTPEndpoints accepts plain-http endpoint URLs. Only for local
	// development: webhooks over http can be read and forged in transit.
	AllowHTTPEndpoints bool
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

	if v, _ := lookup("CARTEIRO_ENCRYPTION_KEY"); v == "" {
		errs = append(errs, errors.New("CARTEIRO_ENCRYPTION_KEY is required (generate one with: openssl rand -base64 32)"))
	} else if key, err := base64.StdEncoding.DecodeString(v); err != nil || len(key) != 32 {
		errs = append(errs, errors.New("CARTEIRO_ENCRYPTION_KEY: must be 32 bytes encoded as base64 (openssl rand -base64 32)"))
	} else {
		cfg.EncryptionKey = key
	}

	if v, ok := lookup("CARTEIRO_ALLOW_HTTP_ENDPOINTS"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("CARTEIRO_ALLOW_HTTP_ENDPOINTS: must be true or false, got %q", v))
		} else {
			cfg.AllowHTTPEndpoints = b
		}
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
