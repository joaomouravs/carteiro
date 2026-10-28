package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func mapLookup(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

const (
	validToken = "0123456789abcdef0123456789abcdef"
	// 32 bytes of 0x01, base64-encoded.
	validKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
)

// withRequired adds the required variables to env, so each test only spells
// out what it is about.
func withRequired(env map[string]string) map[string]string {
	full := map[string]string{
		"CARTEIRO_DATABASE_URL":   "postgres://db/carteiro",
		"CARTEIRO_ADMIN_TOKEN":    validToken,
		"CARTEIRO_ENCRYPTION_KEY": validKey,
	}
	for k, v := range env {
		full[k] = v
	}
	return full
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(mapLookup(withRequired(nil)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://db/carteiro" || cfg.AdminToken != validToken {
		t.Errorf("required values not loaded: %+v", cfg)
	}
	if !bytes.Equal(cfg.EncryptionKey, bytes.Repeat([]byte{1}, 32)) {
		t.Errorf("EncryptionKey = %x", cfg.EncryptionKey)
	}
	if cfg.HTTPAddr != ":8080" || cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 15*time.Second || cfg.AllowHTTPEndpoints {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(mapLookup(withRequired(map[string]string{
		"CARTEIRO_HTTP_ADDR":            "127.0.0.1:9000",
		"CARTEIRO_LOG_LEVEL":            "debug",
		"CARTEIRO_SHUTDOWN_TIMEOUT":     "30s",
		"CARTEIRO_ALLOW_HTTP_ENDPOINTS": "true",
	})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" || cfg.LogLevel != slog.LevelDebug || cfg.ShutdownTimeout != 30*time.Second || !cfg.AllowHTTPEndpoints {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestLoadInvalid(t *testing.T) {
	without := func(key string) map[string]string {
		env := withRequired(nil)
		delete(env, key)
		return env
	}
	tests := []struct {
		name    string
		env     map[string]string
		wantErr []string
	}{
		{"missing database url", without("CARTEIRO_DATABASE_URL"), []string{"CARTEIRO_DATABASE_URL is required"}},
		{"empty database url", withRequired(map[string]string{"CARTEIRO_DATABASE_URL": ""}), []string{"CARTEIRO_DATABASE_URL is required"}},
		{"missing admin token", without("CARTEIRO_ADMIN_TOKEN"), []string{"CARTEIRO_ADMIN_TOKEN is required"}},
		{"short admin token", withRequired(map[string]string{"CARTEIRO_ADMIN_TOKEN": "hunter2"}), []string{"at least 32 characters, got 7"}},
		{"missing encryption key", without("CARTEIRO_ENCRYPTION_KEY"), []string{"CARTEIRO_ENCRYPTION_KEY is required"}},
		{"encryption key not base64", withRequired(map[string]string{"CARTEIRO_ENCRYPTION_KEY": "not base64!"}), []string{"must be 32 bytes"}},
		{"encryption key too short", withRequired(map[string]string{"CARTEIRO_ENCRYPTION_KEY": "AQEBAQ=="}), []string{"must be 32 bytes"}},
		{"allow http not a bool", withRequired(map[string]string{"CARTEIRO_ALLOW_HTTP_ENDPOINTS": "sure"}), []string{"CARTEIRO_ALLOW_HTTP_ENDPOINTS"}},
		{"address without port", withRequired(map[string]string{"CARTEIRO_HTTP_ADDR": "localhost"}), []string{"CARTEIRO_HTTP_ADDR"}},
		{"non-positive timeout", withRequired(map[string]string{"CARTEIRO_SHUTDOWN_TIMEOUT": "0s"}), []string{"must be positive"}},
		{
			name: "every error reported at once",
			env: map[string]string{
				"CARTEIRO_LOG_LEVEL":        "loud",
				"CARTEIRO_SHUTDOWN_TIMEOUT": "soon",
			},
			wantErr: []string{"CARTEIRO_DATABASE_URL", "CARTEIRO_ADMIN_TOKEN", "CARTEIRO_ENCRYPTION_KEY", "CARTEIRO_LOG_LEVEL", "CARTEIRO_SHUTDOWN_TIMEOUT"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(mapLookup(tt.env))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestLoadNeverEchoesAdminToken(t *testing.T) {
	_, err := Load(mapLookup(withRequired(map[string]string{
		"CARTEIRO_ADMIN_TOKEN":    "s3cr3t-but-short",
		"CARTEIRO_ENCRYPTION_KEY": "s3cr3t-key",
	})))
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("error %q must reject the token without echoing it", err)
	}
}
