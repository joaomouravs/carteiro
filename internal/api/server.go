// Package api implements Carteiro's HTTP API.
package api

import (
	"crypto/sha256"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaomouravs/carteiro/internal/store"
)

// Server holds the dependencies shared by every handler.
type Server struct {
	logger *slog.Logger
	db     Pinger
	q      *store.Queries
	// adminTokenHash is the SHA-256 of the admin token; the token itself is
	// not kept in memory longer than needed.
	adminTokenHash [sha256.Size]byte
}

// New returns a Server backed by pool.
func New(logger *slog.Logger, pool *pgxpool.Pool, adminToken string) *Server {
	return &Server{
		logger:         logger,
		db:             pool,
		q:              store.New(pool),
		adminTokenHash: sha256.Sum256([]byte(adminToken)),
	}
}

// Handler returns the root HTTP handler with every route and middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz(s.logger, s.db))

	// Operator endpoints, authenticated with the admin token.
	admin := func(h http.HandlerFunc) http.Handler { return s.requireAdmin(h) }
	mux.Handle("POST /v1/applications", admin(s.createApplication))
	mux.Handle("GET /v1/applications", admin(s.listApplications))
	mux.Handle("GET /v1/applications/{appID}", admin(s.getApplication))
	mux.Handle("POST /v1/applications/{appID}/api-keys", admin(s.createAPIKey))
	mux.Handle("GET /v1/applications/{appID}/api-keys", admin(s.listAPIKeys))
	mux.Handle("DELETE /v1/applications/{appID}/api-keys/{keyID}", admin(s.revokeAPIKey))

	// Producer endpoints, authenticated with an application API key.
	producer := func(h http.HandlerFunc) http.Handler { return s.requireAPIKey(h) }
	mux.Handle("GET /v1/me", producer(s.getMe))

	// Outermost first: the request id exists before anything logs, and a
	// panic anywhere below is still logged and answered.
	return withRequestID(s.logRequests(s.recoverPanics(mux)))
}
