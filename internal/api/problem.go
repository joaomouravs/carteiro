package api

import (
	"net/http"
)

// problem is an error response in the RFC 9457 "Problem Details" format, so
// every error from the API has the same shape and clients need one parser.
type problem struct {
	// Type is "about:blank": the HTTP status already says what went wrong,
	// and RFC 9457 reserves custom type URIs for problems that need more.
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
	// RequestID matches the X-Request-Id header and the server logs, so a
	// user reporting an error can point at the exact request.
	RequestID string `json:"request_id,omitempty"`
	// Errors lists every invalid field at once (extension member).
	Errors []fieldError `json:"errors,omitempty"`
}

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string, errs ...fieldError) {
	p := problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		Instance:  r.URL.Path,
		RequestID: requestIDFrom(r.Context()),
		Errors:    errs,
	}
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	writeJSONType(w, status, "application/problem+json", p)
}

// writeValidation reports semantically invalid input. 422 (RFC 9110) tells a
// client the JSON parsed but its content was rejected, as opposed to 400,
// which this API uses for a body that is not valid JSON at all.
func writeValidation(w http.ResponseWriter, r *http.Request, errs []fieldError) {
	writeProblem(w, r, http.StatusUnprocessableEntity, "the request has invalid fields", errs...)
}

func writeNotFound(w http.ResponseWriter, r *http.Request, what string) {
	writeProblem(w, r, http.StatusNotFound, what+" not found")
}

// writeInternalError logs the real error and returns a generic message:
// internal details such as SQL errors must not reach clients.
func (s *Server) writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.ErrorContext(r.Context(), "internal error",
		"error", err, "request_id", requestIDFrom(r.Context()))
	writeProblem(w, r, http.StatusInternalServerError, "an unexpected error occurred")
}
