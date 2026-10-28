package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

// maxBodyBytes bounds every request body read by decodeJSON (NFR-SEC-05), so
// a client cannot make the server buffer an unbounded amount of memory.
const maxBodyBytes = 64 << 10

func writeJSON(w http.ResponseWriter, status int, body any) {
	writeJSONType(w, status, "application/json", body)
}

func writeJSONType(w http.ResponseWriter, status int, contentType string, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// decodeJSON reads exactly one JSON object from the request body into dst.
// When the body is unacceptable it writes the error response itself and
// returns false, so handlers only need `if !decodeJSON(...) { return }`.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
			writeProblem(w, r, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return false
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	// Rejecting unknown fields turns a misspelled field name into an error
	// instead of a silently ignored field.
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			writeProblem(w, r, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("the request body must not exceed %d bytes", tooLarge.Limit))
		case errors.Is(err, io.EOF):
			writeProblem(w, r, http.StatusBadRequest, "the request body is empty")
		default:
			writeProblem(w, r, http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, r, http.StatusBadRequest, "the request body must contain a single JSON object")
		return false
	}
	return true
}
