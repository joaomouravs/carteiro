package config

import (
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

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(mapLookup(map[string]string{
		"CARTEIRO_DATABASE_URL": "postgres://localhost/carteiro",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		DatabaseURL:     "postgres://localhost/carteiro",
		HTTPAddr:        ":8080",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 15 * time.Second,
	}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(mapLookup(map[string]string{
		"CARTEIRO_DATABASE_URL":     "postgres://db/carteiro",
		"CARTEIRO_HTTP_ADDR":        "127.0.0.1:9000",
		"CARTEIRO_LOG_LEVEL":        "debug",
		"CARTEIRO_SHUTDOWN_TIMEOUT": "30s",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" || cfg.LogLevel != slog.LevelDebug || cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr []string
	}{
		{
			name:    "missing database url",
			env:     map[string]string{},
			wantErr: []string{"CARTEIRO_DATABASE_URL is required"},
		},
		{
			name:    "empty database url",
			env:     map[string]string{"CARTEIRO_DATABASE_URL": ""},
			wantErr: []string{"CARTEIRO_DATABASE_URL is required"},
		},
		{
			name: "address without port",
			env: map[string]string{
				"CARTEIRO_DATABASE_URL": "postgres://db",
				"CARTEIRO_HTTP_ADDR":    "localhost",
			},
			wantErr: []string{"CARTEIRO_HTTP_ADDR"},
		},
		{
			name: "non-positive timeout",
			env: map[string]string{
				"CARTEIRO_DATABASE_URL":     "postgres://db",
				"CARTEIRO_SHUTDOWN_TIMEOUT": "0s",
			},
			wantErr: []string{"must be positive"},
		},
		{
			name: "every error reported at once",
			env: map[string]string{
				"CARTEIRO_LOG_LEVEL":        "loud",
				"CARTEIRO_SHUTDOWN_TIMEOUT": "soon",
			},
			wantErr: []string{"CARTEIRO_DATABASE_URL", "CARTEIRO_LOG_LEVEL", "CARTEIRO_SHUTDOWN_TIMEOUT"},
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
