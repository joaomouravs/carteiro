package api

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"time"
)

type requestIDKey struct{}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// withRequestID tags every request with an id, returned in X-Request-Id and
// attached to its log lines and error responses. An id sent by the client or
// a proxy is kept, so a request can be traced across services.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			id = rand.Text()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// validRequestID accepts short printable ASCII ids only. The id ends up in
// logs, so a client must not be able to inject newlines or huge values.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := range len(id) {
		if id[i] < '!' || id[i] > '~' {
			return false
		}
	}
	return true
}

// statusRecorder captures the status code a handler writes, for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// logRequests writes one access log line per request. It never logs headers
// or bodies, which carry API keys and the admin token (NFR-SEC-06).
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestIDFrom(r.Context()),
		)
	})
}

// recoverPanics turns a panic in a handler into a logged 500, instead of a
// dropped connection with no trace of what happened.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				// http.ErrAbortHandler is the standard way to abort a
				// response on purpose; net/http handles it quietly.
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				s.logger.ErrorContext(r.Context(), "panic in handler",
					"panic", v, "request_id", requestIDFrom(r.Context()))
				writeProblem(w, r, http.StatusInternalServerError, "an unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
