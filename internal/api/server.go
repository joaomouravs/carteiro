// Package api implements Carteiro's HTTP API.
package api

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"net"
	"net/http"
	"net/netip"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaomouravs/carteiro/internal/crypto"
	"github.com/joaomouravs/carteiro/internal/netguard"
	"github.com/joaomouravs/carteiro/internal/store"
)

// Options configures a Server.
type Options struct {
	AdminToken string
	// Cipher encrypts endpoint secrets at rest.
	Cipher *crypto.Cipher
	// AllowHTTPEndpoints accepts plain-http endpoint URLs (development only).
	AllowHTTPEndpoints bool
	// AllowPrivateNetworks accepts endpoints on loopback and private
	// addresses (development only).
	AllowPrivateNetworks bool
	// Resolver looks up endpoint hosts (FR-07). Nil means net.DefaultResolver;
	// tests set a fake one so they do not depend on real DNS.
	Resolver Resolver
}

// Resolver is the part of *net.Resolver the API uses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Server holds the dependencies shared by every handler.
type Server struct {
	logger *slog.Logger
	db     Pinger
	pool   *pgxpool.Pool
	q      *store.Queries
	cipher *crypto.Cipher
	// adminTokenHash is the SHA-256 of the admin token; the token itself is
	// not kept in memory longer than needed.
	adminTokenHash     [sha256.Size]byte
	allowHTTPEndpoints bool
	netPolicy          netguard.Policy
	resolver           Resolver
}

// New returns a Server backed by pool.
func New(logger *slog.Logger, pool *pgxpool.Pool, opts Options) *Server {
	var resolver Resolver = net.DefaultResolver
	if opts.Resolver != nil {
		resolver = opts.Resolver
	}
	return &Server{
		logger:             logger,
		db:                 pool,
		pool:               pool,
		q:                  store.New(pool),
		cipher:             opts.Cipher,
		adminTokenHash:     sha256.Sum256([]byte(opts.AdminToken)),
		allowHTTPEndpoints: opts.AllowHTTPEndpoints,
		netPolicy:          netguard.Policy{AllowPrivateNetworks: opts.AllowPrivateNetworks},
		resolver:           resolver,
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
	mux.Handle("POST /v1/endpoints", producer(s.createEndpoint))
	mux.Handle("GET /v1/endpoints", producer(s.listEndpoints))
	mux.Handle("GET /v1/endpoints/{endpointID}", producer(s.getEndpoint))
	mux.Handle("PATCH /v1/endpoints/{endpointID}", producer(s.updateEndpoint))
	mux.Handle("DELETE /v1/endpoints/{endpointID}", producer(s.deleteEndpoint))
	mux.Handle("POST /v1/endpoints/{endpointID}/enable", producer(s.enableEndpoint))
	mux.Handle("POST /v1/endpoints/{endpointID}/replay", producer(s.replayEndpoint))
	mux.Handle("POST /v1/events", producer(s.createEvent))
	mux.Handle("GET /v1/events", producer(s.listEvents))
	mux.Handle("GET /v1/events/{eventID}", producer(s.getEvent))
	mux.Handle("GET /v1/events/{eventID}/deliveries", producer(s.listEventDeliveries))
	mux.Handle("GET /v1/deliveries/{deliveryID}", producer(s.getDelivery))
	mux.Handle("GET /v1/deliveries/{deliveryID}/attempts", producer(s.listAttempts))
	mux.Handle("POST /v1/deliveries/{deliveryID}/replay", producer(s.replayDelivery))

	// Outermost first: the request id exists before anything logs, and a
	// panic anywhere below is still logged and answered.
	return withRequestID(s.logRequests(s.recoverPanics(mux)))
}
