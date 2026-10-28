package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/joaomouravs/carteiro/internal/crypto"
	"github.com/joaomouravs/carteiro/internal/store"
)

type endpointResponse struct {
	ID          uuid.UUID `json:"id"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	EventTypes  []string  `json:"event_types"`
	// Status is active or disabled; DisabledReason says why: "gone" (the
	// receiver answered 410) or "failing" (FR-08).
	Status         string  `json:"status"`
	DisabledReason *string `json:"disabled_reason"`
	// ConsecutiveFailures and FailingSince describe the current failure
	// streak; FailingSince is null while the endpoint is healthy.
	ConsecutiveFailures int32      `json:"consecutive_failures"`
	FailingSince        *time.Time `json:"failing_since"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	// Secret is set only in the response that creates the endpoint; the
	// receiver needs it to verify signatures.
	Secret string `json:"secret,omitempty"`
}

func toEndpointResponse(e store.Endpoint) endpointResponse {
	types := e.EventTypes
	if types == nil {
		types = []string{} // always a JSON array, never null
	}
	return endpointResponse{
		ID:                  e.ID,
		URL:                 e.URL,
		Description:         e.Description,
		EventTypes:          types,
		Status:              e.Status,
		DisabledReason:      e.DisabledReason,
		ConsecutiveFailures: e.ConsecutiveFailures,
		FailingSince:        e.FailingSince,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
	}
}

func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL         string   `json:"url"`
		Description string   `json:"description"`
		EventTypes  []string `json:"event_types"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	endpointURL, errs := s.validateEndpointURL(r.Context(), in.URL)
	desc, descErrs := validateDescription(in.Description)
	types, typeErrs := validateEventTypes(in.EventTypes)
	if errs = append(append(errs, descErrs...), typeErrs...); len(errs) > 0 {
		writeValidation(w, r, errs)
		return
	}

	ctx := r.Context()
	secret := crypto.NewEndpointSecret()
	var ep store.Endpoint
	// The endpoint and its secret are created together or not at all: an
	// endpoint without a secret could never have a delivery signed.
	err := store.InTx(ctx, s.pool, func(q *store.Queries) error {
		var err error
		ep, err = q.CreateEndpoint(ctx, store.CreateEndpointParams{
			ApplicationID: applicationIDFrom(ctx),
			URL:           endpointURL,
			Description:   desc,
			EventTypes:    types,
		})
		if err != nil {
			return err
		}
		return q.CreateEndpointSecret(ctx, store.CreateEndpointSecretParams{
			EndpointID: ep.ID,
			// The endpoint id is the associated data: this ciphertext only
			// decrypts as the secret of this endpoint.
			SecretCiphertext: s.cipher.Encrypt(secret, ep.ID[:]),
		})
	})
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}

	resp := toEndpointResponse(ep)
	resp.Secret = crypto.FormatEndpointSecret(secret)
	w.Header().Set("Location", "/v1/endpoints/"+ep.ID.String())
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, resp)
}

// endpointFromPath loads the endpoint named in the URL, scoped to the caller's
// application. It writes the error response itself and reports false when
// there is no such endpoint.
func (s *Server) endpointFromPath(w http.ResponseWriter, r *http.Request) (store.Endpoint, bool) {
	id, ok := pathID(r, "endpointID")
	if !ok {
		writeNotFound(w, r, "endpoint")
		return store.Endpoint{}, false
	}
	ep, err := s.q.GetEndpoint(r.Context(), store.GetEndpointParams{ID: id, ApplicationID: applicationIDFrom(r.Context())})
	if errors.Is(err, pgx.ErrNoRows) {
		writeNotFound(w, r, "endpoint")
		return store.Endpoint{}, false
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return store.Endpoint{}, false
	}
	return ep, true
}

func (s *Server) getEndpoint(w http.ResponseWriter, r *http.Request) {
	if ep, ok := s.endpointFromPath(w, r); ok {
		writeJSON(w, http.StatusOK, toEndpointResponse(ep))
	}
}

func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request) {
	page, errs := parsePage(r)
	if errs != nil {
		writeValidation(w, r, errs)
		return
	}
	rows, err := s.q.ListEndpoints(r.Context(), store.ListEndpointsParams{
		ApplicationID: applicationIDFrom(r.Context()),
		Before:        page.before,
		Limit:         int32(page.limit + 1), //nolint:gosec // limit is at most maxPageSize, validated by parsePage
	})
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, paginate(rows, page.limit,
		func(e store.Endpoint) uuid.UUID { return e.ID }, toEndpointResponse))
}

// updateEndpoint applies a partial update: fields absent from the body (or
// null) keep their value. An empty event_types array is a change, to "all
// event types".
func (s *Server) updateEndpoint(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "endpointID")
	if !ok {
		writeNotFound(w, r, "endpoint")
		return
	}
	var in struct {
		URL         *string   `json:"url"`
		Description *string   `json:"description"`
		EventTypes  *[]string `json:"event_types"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}

	params := store.UpdateEndpointParams{ID: id, ApplicationID: applicationIDFrom(r.Context())}
	var errs []fieldError
	if in.URL != nil {
		u, e := s.validateEndpointURL(r.Context(), *in.URL)
		params.URL, errs = &u, append(errs, e...)
	}
	if in.Description != nil {
		d, e := validateDescription(*in.Description)
		params.Description, errs = &d, append(errs, e...)
	}
	if in.EventTypes != nil {
		// Non-nil even when empty: nil would mean "unchanged" to the query.
		t, e := validateEventTypes(*in.EventTypes)
		params.EventTypes, errs = t, append(errs, e...)
	}
	if len(errs) > 0 {
		writeValidation(w, r, errs)
		return
	}

	ep, err := s.q.UpdateEndpoint(r.Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		writeNotFound(w, r, "endpoint")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toEndpointResponse(ep))
}

// deleteEndpoint also deletes the endpoint's secrets, deliveries and attempts
// (ON DELETE CASCADE).
func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "endpointID")
	if !ok {
		writeNotFound(w, r, "endpoint")
		return
	}
	n, err := s.q.DeleteEndpoint(r.Context(), store.DeleteEndpointParams{ID: id, ApplicationID: applicationIDFrom(r.Context())})
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	if n == 0 {
		writeNotFound(w, r, "endpoint")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// enableEndpoint re-enables a disabled endpoint (FR-09). Deliveries killed
// when it was disabled stay dead; replay them once the receiver is fixed.
func (s *Server) enableEndpoint(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "endpointID")
	if !ok {
		writeNotFound(w, r, "endpoint")
		return
	}
	ep, err := s.q.EnableEndpoint(r.Context(), store.EnableEndpointParams{ID: id, ApplicationID: applicationIDFrom(r.Context())})
	if errors.Is(err, pgx.ErrNoRows) {
		writeNotFound(w, r, "endpoint")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toEndpointResponse(ep))
}

// replayEndpoint replays every dead delivery of the endpoint created since a
// time (FR-18): the usual step after fixing a receiver and enabling its
// endpoint again.
func (s *Server) replayEndpoint(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpointFromPath(w, r)
	if !ok {
		return
	}
	var in struct {
		// A string rather than a time.Time, so a malformed value is a
		// field error (422) instead of a generic JSON error.
		Since string `json:"since"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	since, err := time.Parse(time.RFC3339, in.Since)
	switch {
	case in.Since == "":
		writeValidation(w, r, []fieldError{{"since", "is required"}})
		return
	case err != nil:
		writeValidation(w, r, []fieldError{{"since", "must be an RFC 3339 timestamp, such as 2026-10-28T09:00:00Z"}})
		return
	case since.After(time.Now()):
		writeValidation(w, r, []fieldError{{"since", "must not be in the future"}})
		return
	}
	if ep.Status != "active" {
		writeProblem(w, r, http.StatusConflict, "the endpoint is disabled; enable it before replaying")
		return
	}
	n, err := s.q.ReplayDeadDeliveries(r.Context(), store.ReplayDeadDeliveriesParams{EndpointID: ep.ID, Since: since})
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int64{"replayed": n})
}

// maxSecretGracePeriod bounds how long a rotated-out secret may stay valid.
const maxSecretGracePeriod = 7 * 24 * time.Hour

// rotateSecret gives the endpoint a new signing secret (FR-06) and returns it,
// once, as creating the endpoint does. Until the grace period ends,
// deliveries are signed with the new and the old secrets, so the receiver
// can switch whenever it likes in that window. A grace period of 0 revokes
// the old secrets at once, for a secret that leaked.
func (s *Server) rotateSecret(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "endpointID")
	if !ok {
		writeNotFound(w, r, "endpoint")
		return
	}
	var in struct {
		GracePeriodSeconds *int64 `json:"grace_period_seconds"`
	}
	// The body is optional: without one, the configured grace period applies.
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	grace := s.secretGracePeriod
	if in.GracePeriodSeconds != nil {
		secs := *in.GracePeriodSeconds
		if secs < 0 || secs > int64(maxSecretGracePeriod/time.Second) {
			writeValidation(w, r, []fieldError{{"grace_period_seconds",
				fmt.Sprintf("must be from 0 to %d (7 days)", int64(maxSecretGracePeriod/time.Second))}})
			return
		}
		grace = time.Duration(secs) * time.Second
	}

	ctx := r.Context()
	secret := crypto.NewEndpointSecret()
	var ep store.Endpoint
	err := store.InTx(ctx, s.pool, func(q *store.Queries) error {
		var err error
		if ep, err = q.LockEndpoint(ctx, store.LockEndpointParams{ID: id, ApplicationID: applicationIDFrom(ctx)}); err != nil {
			return err
		}
		// Retire first: the unique index allows one current secret, and
		// the new one becomes current below.
		if err := q.RetireEndpointSecrets(ctx, store.RetireEndpointSecretsParams{EndpointID: ep.ID, GraceMs: grace.Milliseconds()}); err != nil {
			return err
		}
		return q.CreateEndpointSecret(ctx, store.CreateEndpointSecretParams{
			EndpointID:       ep.ID,
			SecretCiphertext: s.cipher.Encrypt(secret, ep.ID[:]),
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeNotFound(w, r, "endpoint")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}

	resp := toEndpointResponse(ep)
	resp.Secret = crypto.FormatEndpointSecret(secret)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}
