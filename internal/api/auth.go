package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/joaomouravs/carteiro/internal/crypto"
)

// touchInterval limits how often last_used_at is written. Writing it on
// every request would turn each authenticated read into a database write.
const touchInterval = time.Minute

type applicationIDKey struct{}

// applicationIDFrom returns the application authenticated by requireAPIKey.
func applicationIDFrom(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(applicationIDKey{}).(uuid.UUID)
	return id
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// requireAdmin guards the operator endpoints with the static admin token (FR-01).
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		// Comparing fixed-size SHA-256 digests in constant time means the
		// response time reveals neither how many leading characters of a
		// guess were right nor the length of the real token.
		sum := sha256.Sum256([]byte(token))
		if !ok || subtle.ConstantTimeCompare(sum[:], s.adminTokenHash[:]) != 1 {
			writeProblem(w, r, http.StatusUnauthorized, "a valid admin token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAPIKey authenticates a producer by API key and scopes the request to
// its application (FR-03).
func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeProblem(w, r, http.StatusUnauthorized, "an API key is required")
			return
		}
		// The lookup is by hash, so the database never sees the key itself.
		key, err := s.q.GetActiveAPIKeyByHash(r.Context(), crypto.HashAPIKey(token))
		if errors.Is(err, pgx.ErrNoRows) {
			// Unknown and revoked keys get the same answer, so a client
			// cannot learn whether a key ever existed.
			writeProblem(w, r, http.StatusUnauthorized, "the API key is invalid or revoked")
			return
		}
		if err != nil {
			s.writeInternalError(w, r, err)
			return
		}

		if key.LastUsedAt == nil || time.Since(*key.LastUsedAt) > touchInterval {
			// Bookkeeping only: a failure must not fail the request.
			if err := s.q.TouchAPIKey(r.Context(), key.ID); err != nil {
				s.logger.WarnContext(r.Context(), "update api key last_used_at", "error", err)
			}
		}

		ctx := context.WithValue(r.Context(), applicationIDKey{}, key.ApplicationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
