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
	// AllowPrivateNetworks lets endpoints and deliveries reach loopback and
	// private addresses, so receivers on localhost or in the same compose
	// network work. Only for local development: in production it would let
	// producers reach Carteiro's internal network (SSRF).
	AllowPrivateNetworks bool
	// HTTPAddr is the host:port the API listens on.
	HTTPAddr string
	// LogLevel is the minimum level written to the log.
	LogLevel slog.Level
	// ShutdownTimeout bounds how long in-flight requests get to finish after
	// SIGINT or SIGTERM.
	ShutdownTimeout time.Duration

	// WorkerConcurrency is how many deliveries one worker sends at once.
	WorkerConcurrency int
	// DeliveryTimeout bounds each delivery's HTTP request. The lease is
	// derived from it (ADR 0003).
	DeliveryTimeout time.Duration
	// PollInterval is how long an idle worker waits before checking the
	// queue again.
	PollInterval time.Duration
	// RetryBase, RetryCap and MaxAttempts are the retry schedule (FR-14,
	// FR-15; docs/architecture.md, "Retry schedule").
	RetryBase   time.Duration
	RetryCap    time.Duration
	MaxAttempts int
	// DisableAfter and DisableMinFailures disable an endpoint that keeps
	// failing (FR-08): both must be reached.
	DisableAfter       time.Duration
	DisableMinFailures int
	// SecretGracePeriod is how long a rotated-out endpoint secret stays
	// valid by default (FR-06).
	SecretGracePeriod time.Duration
	// Retention is how long events and attempts are kept (FR-21).
	Retention time.Duration
}

// Bounds for the integer settings. They exist to catch typos, such as an
// extra zero, before they reach production.
const (
	maxWorkerConcurrency  = 1000
	maxMaxAttempts        = 100
	maxDisableMinFailures = 1000
)

// minAdminTokenLen rejects tokens short enough to guess. 32 characters of a
// random string such as `openssl rand -hex 32` output is far beyond that.
const minAdminTokenLen = 32

// Load reads the configuration through lookup, which is os.LookupEnv in
// production and a map in tests. It reports every invalid variable at once,
// so a misconfigured deploy is fixed in one round instead of one per variable.
func Load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		HTTPAddr:           ":8080",
		LogLevel:           slog.LevelInfo,
		ShutdownTimeout:    15 * time.Second,
		WorkerConcurrency:  10,
		DeliveryTimeout:    15 * time.Second,
		PollInterval:       time.Second,
		RetryBase:          30 * time.Second,
		RetryCap:           6 * time.Hour,
		MaxAttempts:        12,
		DisableAfter:       5 * 24 * time.Hour,
		DisableMinFailures: 10,
		SecretGracePeriod:  24 * time.Hour,
		Retention:          30 * 24 * time.Hour,
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

	parseBool := func(name string, dst *bool) {
		v, ok := lookup(name)
		if !ok {
			return
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: must be true or false, got %q", name, v))
			return
		}
		*dst = b
	}
	parseBool("CARTEIRO_ALLOW_HTTP_ENDPOINTS", &cfg.AllowHTTPEndpoints)
	parseBool("CARTEIRO_ALLOW_PRIVATE_NETWORKS", &cfg.AllowPrivateNetworks)

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

	parseDuration := func(name string, dst *time.Duration) {
		v, ok := lookup(name)
		if !ok {
			return
		}
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		case d <= 0:
			errs = append(errs, fmt.Errorf("%s: must be positive, got %s", name, d))
		default:
			*dst = d
		}
	}
	parseDuration("CARTEIRO_SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout)
	parseDuration("CARTEIRO_DELIVERY_TIMEOUT", &cfg.DeliveryTimeout)
	parseDuration("CARTEIRO_POLL_INTERVAL", &cfg.PollInterval)
	parseDuration("CARTEIRO_RETRY_BASE", &cfg.RetryBase)
	parseDuration("CARTEIRO_RETRY_CAP", &cfg.RetryCap)
	parseDuration("CARTEIRO_DISABLE_AFTER", &cfg.DisableAfter)
	parseDuration("CARTEIRO_SECRET_GRACE_PERIOD", &cfg.SecretGracePeriod)
	parseDuration("CARTEIRO_RETENTION", &cfg.Retention)

	parseInt := func(name string, dst *int, maxValue int) {
		v, ok := lookup(name)
		if !ok {
			return
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxValue {
			errs = append(errs, fmt.Errorf("%s: must be a whole number from 1 to %d, got %q", name, maxValue, v))
			return
		}
		*dst = n
	}
	parseInt("CARTEIRO_WORKER_CONCURRENCY", &cfg.WorkerConcurrency, maxWorkerConcurrency)
	parseInt("CARTEIRO_MAX_ATTEMPTS", &cfg.MaxAttempts, maxMaxAttempts)
	parseInt("CARTEIRO_DISABLE_MIN_FAILURES", &cfg.DisableMinFailures, maxDisableMinFailures)

	if cfg.RetryCap < cfg.RetryBase {
		errs = append(errs, fmt.Errorf("CARTEIRO_RETRY_CAP (%s) must not be less than CARTEIRO_RETRY_BASE (%s)", cfg.RetryCap, cfg.RetryBase))
	}

	return cfg, errors.Join(errs...)
}
