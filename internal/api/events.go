package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/joaomouravs/carteiro/internal/store"
)

const (
	// maxEventBodyBytes is the default payload limit of NFR-SEC-05.
	maxEventBodyBytes = 256 << 10
	// maxIdempotencyKeyLen matches common practice (Stripe uses 255).
	maxIdempotencyKeyLen = 255
)

// SQLSTATEs PostgreSQL returns for JSON that encoding/json accepts but jsonb
// cannot store, such as "\u0000" inside a string.
const (
	pgUntranslatableCharacter   = "22P05"
	pgInvalidTextRepresentation = "22P02"
)

type eventResponse struct {
	ID        uuid.UUID `json:"id"`
	EventType string    `json:"event_type"`
	// Payload is returned as stored: normalized by PostgreSQL's jsonb.
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey *string         `json:"idempotency_key"`
	CreatedAt      time.Time       `json:"created_at"`
}

func toEventResponse(e store.Event) eventResponse {
	return eventResponse{
		ID:             e.ID,
		EventType:      e.EventType,
		Payload:        e.Payload,
		IdempotencyKey: e.IdempotencyKey,
		CreatedAt:      e.CreatedAt,
	}
}

// idempotencyKey reads the optional Idempotency-Key header.
func idempotencyKey(r *http.Request) (*string, []fieldError) {
	v := r.Header.Get("Idempotency-Key")
	if v == "" {
		return nil, nil
	}
	if len(v) > maxIdempotencyKeyLen || !printableASCII(v) {
		return nil, []fieldError{{"Idempotency-Key", "must be 1 to 255 printable ASCII characters"}}
	}
	return &v, nil
}

func printableASCII(s string) bool {
	for i := range len(s) {
		if s[i] < ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// isJSONObject reports whether raw, already known to be valid JSON, is an
// object rather than an array, string, number, boolean or null.
func isJSONObject(raw json.RawMessage) bool {
	b := bytes.TrimLeft(raw, " \t\r\n")
	return len(b) > 0 && b[0] == '{'
}

// createEvent publishes an event (FR-10 to FR-12).
//
// A new event answers 202 Accepted: it is stored and its deliveries are
// queued, but nothing has been delivered yet. With an Idempotency-Key that
// the application already used, the original event is returned with 200 if
// the request is the same (a retry), and 409 Conflict if it differs.
func (s *Server) createEvent(w http.ResponseWriter, r *http.Request) {
	key, errs := idempotencyKey(r)

	var in struct {
		EventType string `json:"event_type"`
		// RawMessage keeps the payload as bytes. Decoding it into a
		// map[string]any would turn large integers into float64 and lose
		// precision; PostgreSQL stores numbers exactly.
		Payload json.RawMessage `json:"payload"`
	}
	if !decodeJSONLimit(w, r, &in, maxEventBodyBytes) {
		return
	}
	if !validEventType(in.EventType) {
		errs = append(errs, fieldError{"event_type",
			"must be 1 to 200 letters, digits, '.', '_' or '-', starting with a letter or digit"})
	}
	switch {
	case len(in.Payload) == 0:
		errs = append(errs, fieldError{"payload", "is required"})
	case !isJSONObject(in.Payload):
		errs = append(errs, fieldError{"payload", "must be a JSON object"})
	}
	if len(errs) > 0 {
		writeValidation(w, r, errs)
		return
	}

	ctx := r.Context()
	appID := applicationIDFrom(ctx)
	var (
		event    store.Event
		replayed bool
		conflict bool
	)
	// The event and its deliveries are written in one transaction: an
	// accepted event always has its deliveries, so it can never be lost
	// between being saved and being queued (ADR 0001).
	err := store.InTx(ctx, s.pool, func(q *store.Queries) error {
		var err error
		event, err = q.InsertEvent(ctx, store.InsertEventParams{
			ApplicationID:  appID,
			EventType:      in.EventType,
			Payload:        in.Payload,
			IdempotencyKey: key,
		})
		if err == nil {
			_, err = q.CreateDeliveries(ctx, store.CreateDeliveriesParams{
				EventID: event.ID, ApplicationID: appID, EventType: in.EventType,
			})
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// No row inserted: the key is taken. Retry or conflict?
		prev, err := q.GetEventByIdempotencyKey(ctx, store.GetEventByIdempotencyKeyParams{
			Payload: in.Payload, ApplicationID: appID, IdempotencyKey: key,
		})
		if err != nil {
			return err
		}
		event = prev.Event
		if prev.SamePayload && prev.Event.EventType == in.EventType {
			replayed = true
		} else {
			conflict = true
		}
		return nil
	})

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == pgUntranslatableCharacter || pgErr.Code == pgInvalidTextRepresentation) {
		writeValidation(w, r, []fieldError{{"payload", "cannot be stored: " + pgErr.Message}})
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}

	switch {
	case conflict:
		writeProblem(w, r, http.StatusConflict,
			"the Idempotency-Key was already used with a different event_type or payload")
	case replayed:
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, http.StatusOK, toEventResponse(event))
	default:
		writeJSON(w, http.StatusAccepted, toEventResponse(event))
	}
}
