package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/joaomouravs/carteiro/internal/store"
)

// deliveryResponse is one event's delivery to one endpoint (FR-17). The lease
// is left out: it is the worker's bookkeeping, and status says what matters.
type deliveryResponse struct {
	ID         uuid.UUID `json:"id"`
	EventID    uuid.UUID `json:"event_id"`
	EndpointID uuid.UUID `json:"endpoint_id"`
	// Status is pending, in_flight, succeeded or dead.
	Status       string `json:"status"`
	AttemptCount int32  `json:"attempt_count"`
	// NextAttemptAt is when a pending delivery is due; null otherwise.
	NextAttemptAt *time.Time `json:"next_attempt_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func toDeliveryResponse(d store.Delivery) deliveryResponse {
	resp := deliveryResponse{
		ID:            d.ID,
		EventID:       d.EventID,
		EndpointID:    d.EndpointID,
		Status:        d.Status,
		AttemptCount:  d.AttemptCount,
		NextAttemptAt: d.NextAttemptAt,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
	// A claimed delivery keeps the next_attempt_at it was claimed at, which
	// is now in the past. Only a pending delivery has a meaningful one.
	if d.Status != "pending" {
		resp.NextAttemptAt = nil
	}
	return resp
}

// attemptResponse is one HTTP attempt (FR-16).
type attemptResponse struct {
	ID            uuid.UUID `json:"id"`
	AttemptNumber int32     `json:"attempt_number"`
	StartedAt     time.Time `json:"started_at"`
	DurationMs    int32     `json:"duration_ms"`
	// ResponseStatus and ResponseBody are null when no response arrived:
	// a timeout, a refused connection, or a blocked address.
	ResponseStatus *int32 `json:"response_status"`
	// ResponseBody holds the first 4 KiB of the body.
	ResponseBody *string `json:"response_body"`
	Error        *string `json:"error"`
}

func toAttemptResponse(a store.Attempt) attemptResponse {
	return attemptResponse{
		ID:             a.ID,
		AttemptNumber:  a.AttemptNumber,
		StartedAt:      a.StartedAt,
		DurationMs:     a.DurationMs,
		ResponseStatus: a.ResponseStatus,
		ResponseBody:   a.ResponseBody,
		Error:          a.Error,
	}
}

func (s *Server) deliveryFromPath(w http.ResponseWriter, r *http.Request) (store.Delivery, bool) {
	id, ok := pathID(r, "deliveryID")
	if !ok {
		writeNotFound(w, r, "delivery")
		return store.Delivery{}, false
	}
	d, err := s.q.GetDelivery(r.Context(), store.GetDeliveryParams{ID: id, ApplicationID: applicationIDFrom(r.Context())})
	if errors.Is(err, pgx.ErrNoRows) {
		writeNotFound(w, r, "delivery")
		return store.Delivery{}, false
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return store.Delivery{}, false
	}
	return d, true
}

func (s *Server) getDelivery(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.deliveryFromPath(w, r); ok {
		writeJSON(w, http.StatusOK, toDeliveryResponse(d))
	}
}

// listAttempts lists a delivery's attempts, newest first.
func (s *Server) listAttempts(w http.ResponseWriter, r *http.Request) {
	page, errs := parsePage(r)
	if errs != nil {
		writeValidation(w, r, errs)
		return
	}
	d, ok := s.deliveryFromPath(w, r)
	if !ok {
		return
	}
	rows, err := s.q.ListAttempts(r.Context(), store.ListAttemptsParams{
		DeliveryID: d.ID,
		Before:     page.before,
		Limit:      int32(page.limit + 1), //nolint:gosec // limit is at most maxPageSize, validated by parsePage
	})
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, paginate(rows, page.limit,
		func(a store.Attempt) uuid.UUID { return a.ID }, toAttemptResponse))
}

// replayDelivery sends one finished delivery again (FR-18): a dead one after
// its receiver is fixed, or a succeeded one whose receiver lost the data.
func (s *Server) replayDelivery(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deliveryFromPath(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if d.Status == "pending" || d.Status == "in_flight" {
		writeProblem(w, r, http.StatusConflict, "the delivery is already queued")
		return
	}
	ep, err := s.q.GetEndpoint(ctx, store.GetEndpointParams{ID: d.EndpointID, ApplicationID: applicationIDFrom(ctx)})
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	if ep.Status != "active" {
		writeProblem(w, r, http.StatusConflict, "the delivery's endpoint is disabled; enable it before replaying")
		return
	}
	replayed, err := s.q.ReplayDelivery(ctx, d.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The checks above passed, but the delivery or its endpoint changed
		// before the update ran.
		writeProblem(w, r, http.StatusConflict, "the delivery changed while being replayed; try again")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toDeliveryResponse(replayed))
}
