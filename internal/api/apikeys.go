package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/joaomouravs/carteiro/internal/crypto"
	"github.com/joaomouravs/carteiro/internal/store"
)

// pgForeignKeyViolation is the SQLSTATE PostgreSQL returns when a row points
// at a parent that does not exist.
const pgForeignKeyViolation = "23503"

type apiKeyResponse struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	// Key is set only in the response that creates the key. Carteiro keeps
	// just its hash, so it can never be shown again.
	Key string `json:"key,omitempty"`
}

func toAPIKeyResponse(k store.APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID:         k.ID,
		Name:       k.Name,
		Prefix:     k.Prefix,
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
	}
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	appID, ok := pathID(r, "appID")
	if !ok {
		writeNotFound(w, r, "application")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	name, errs := validateName("name", in.Name, false)
	if errs != nil {
		writeValidation(w, r, errs)
		return
	}

	key := crypto.NewAPIKey()
	row, err := s.q.CreateAPIKey(r.Context(), store.CreateAPIKeyParams{
		ApplicationID: appID,
		Name:          name,
		Prefix:        key.Prefix,
		Hash:          key.Hash,
	})
	// The foreign key answers "does the application exist?" in the same
	// statement as the insert, so there is no gap for a concurrent delete.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
		writeNotFound(w, r, "application")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}

	resp := toAPIKeyResponse(row)
	resp.Key = key.Key
	// The body holds a secret: no proxy or browser may cache it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, resp)
}

// listAPIKeys is not paginated: an application has a handful of keys.
func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	appID, ok := pathID(r, "appID")
	if !ok {
		writeNotFound(w, r, "application")
		return
	}
	if _, err := s.q.GetApplication(r.Context(), appID); errors.Is(err, pgx.ErrNoRows) {
		writeNotFound(w, r, "application")
		return
	} else if err != nil {
		s.writeInternalError(w, r, err)
		return
	}

	rows, err := s.q.ListAPIKeys(r.Context(), appID)
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	resp := listResponse[apiKeyResponse]{Data: make([]apiKeyResponse, 0, len(rows))}
	for _, k := range rows {
		resp.Data = append(resp.Data, toAPIKeyResponse(k))
	}
	writeJSON(w, http.StatusOK, resp)
}

// revokeAPIKey is idempotent: revoking an already revoked key also answers
// 204, so a client retrying after a timeout gets the same result.
func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	appID, ok1 := pathID(r, "appID")
	keyID, ok2 := pathID(r, "keyID")
	if !ok1 || !ok2 {
		writeNotFound(w, r, "API key")
		return
	}
	// Matching on the application too means a key id from another
	// application is simply "not found" here.
	_, err := s.q.RevokeAPIKey(r.Context(), store.RevokeAPIKeyParams{ID: keyID, ApplicationID: appID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeNotFound(w, r, "API key")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
