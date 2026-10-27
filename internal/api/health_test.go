package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestProbes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dbDown := fakePinger{err: errors.New("connection refused")}

	tests := []struct {
		name   string
		db     Pinger
		path   string
		status int
	}{
		{"liveness with database up", fakePinger{}, "/healthz", http.StatusOK},
		{"liveness ignores database", dbDown, "/healthz", http.StatusOK},
		{"readiness with database up", fakePinger{}, "/readyz", http.StatusOK},
		{"readiness with database down", dbDown, "/readyz", http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewHandler(logger, tt.db).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
		})
	}
}

func TestProbesRejectOtherMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), fakePinger{})
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
