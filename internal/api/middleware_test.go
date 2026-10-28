package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestID(t *testing.T) {
	h := (&Server{logger: discard, db: fakePinger{}}).Handler()

	tests := []struct {
		name     string
		incoming string
		keep     bool
	}{
		{"generated when absent", "", false},
		{"kept when valid", "trace-abc-123", true},
		{"replaced when too long", strings.Repeat("x", 65), false},
		{"replaced when it has spaces", "two words", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/healthz", nil)
			if tt.incoming != "" {
				req.Header.Set("X-Request-Id", tt.incoming)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get("X-Request-Id")
			if !validRequestID(got) {
				t.Fatalf("response has invalid request id %q", got)
			}
			if (got == tt.incoming) != tt.keep {
				t.Errorf("request id = %q, incoming %q, keep = %v", got, tt.incoming, tt.keep)
			}
		})
	}
}

func TestProblemCarriesRequestID(t *testing.T) {
	h := (&Server{logger: discard, db: fakePinger{}}).Handler()
	req := httptest.NewRequest("GET", "/v1/applications", nil)
	req.Header.Set("X-Request-Id", "req-42")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	p := wantProblem(t, rec, http.StatusUnauthorized)
	if p.RequestID != "req-42" || p.Instance != "/v1/applications" {
		t.Errorf("problem = %+v", p)
	}
}

func TestRecoverPanics(t *testing.T) {
	s := &Server{logger: discard}
	h := withRequestID(s.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	p := wantProblem(t, rec, http.StatusInternalServerError)
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("panic value leaked to the client")
	}
	if p.RequestID == "" {
		t.Error("500 without request id")
	}
}
