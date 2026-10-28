package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

// listResponse is the envelope of every paginated list.
type listResponse[T any] struct {
	Data []T `json:"data"`
	// NextCursor is null on the last page.
	NextCursor *string `json:"next_cursor"`
}

// pageRequest is a parsed ?cursor=&limit= query.
type pageRequest struct {
	// before is the id the page starts below; nil for the first page.
	before *uuid.UUID
	limit  int
}

// parsePage reads the pagination query parameters. I paginate by cursor,
// not by offset: OFFSET makes the database walk and discard every skipped
// row, and rows inserted while a client pages through make it skip or
// repeat items. A cursor is a position, so neither happens.
func parsePage(r *http.Request) (pageRequest, []fieldError) {
	p := pageRequest{limit: defaultPageSize}
	var errs []fieldError

	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageSize {
			errs = append(errs, fieldError{"limit", "must be an integer between 1 and " + strconv.Itoa(maxPageSize)})
		} else {
			p.limit = n
		}
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		id, err := decodeCursor(v)
		if err != nil {
			errs = append(errs, fieldError{"cursor", "is invalid; use a next_cursor value returned by the API"})
		} else {
			p.before = &id
		}
	}
	return p, errs
}

// paginate trims rows, fetched with limit+1, to one page and computes the
// cursor of the next one. Fetching one extra row is how it knows whether a
// next page exists without a second COUNT query.
func paginate[R, T any](rows []R, limit int, id func(R) uuid.UUID, conv func(R) T) listResponse[T] {
	resp := listResponse[T]{Data: make([]T, 0, min(len(rows), limit))}
	if len(rows) > limit {
		rows = rows[:limit]
		c := encodeCursor(id(rows[len(rows)-1]))
		resp.NextCursor = &c
	}
	for _, row := range rows {
		resp.Data = append(resp.Data, conv(row))
	}
	return resp
}

// Cursors are opaque to clients (base64url of the id bytes), so they cannot be
// hand-built and the encoding can change without breaking anyone.
func encodeCursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

func decodeCursor(s string) (uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return uuid.UUID{}, err
	}
	if len(b) != len(uuid.UUID{}) {
		return uuid.UUID{}, errors.New("wrong cursor length")
	}
	return uuid.UUID(b), nil
}
