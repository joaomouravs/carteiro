package api

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/joaomouravs/carteiro/internal/store"
)

const maxNameLen = 200

// applicationResponse is the API representation of an application. Handlers
// return these response types, never the sqlc models, so a new database
// column cannot leak into the API by accident.
type applicationResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func toApplicationResponse(a store.Application) applicationResponse {
	return applicationResponse{ID: a.ID, Name: a.Name, CreatedAt: a.CreatedAt}
}

// validateName trims a name and checks its length; required says whether an
// empty name is an error.
func validateName(field, name string, required bool) (string, []fieldError) {
	name = strings.TrimSpace(name)
	switch {
	case name == "" && required:
		return "", []fieldError{{field, "is required"}}
	case utf8.RuneCountInString(name) > maxNameLen:
		return "", []fieldError{{field, "must have at most 200 characters"}}
	}
	return name, nil
}

// pathID parses an id from the URL path. A malformed id answers 404, not 400:
// from the client's point of view, no resource lives at that URL.
func pathID(r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	return id, err == nil
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	name, errs := validateName("name", in.Name, true)
	if errs != nil {
		writeValidation(w, r, errs)
		return
	}

	app, err := s.q.CreateApplication(r.Context(), name)
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/applications/"+app.ID.String())
	writeJSON(w, http.StatusCreated, toApplicationResponse(app))
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "appID")
	if !ok {
		writeNotFound(w, r, "application")
		return
	}
	app, err := s.q.GetApplication(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeNotFound(w, r, "application")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(app))
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	page, errs := parsePage(r)
	if errs != nil {
		writeValidation(w, r, errs)
		return
	}
	rows, err := s.q.ListApplications(r.Context(), store.ListApplicationsParams{
		Before: page.before,
		Limit:  int32(page.limit + 1), //nolint:gosec // limit is at most maxPageSize, validated by parsePage
	})
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, paginate(rows, page.limit,
		func(a store.Application) uuid.UUID { return a.ID }, toApplicationResponse))
}

// getMe returns the application the API key belongs to, so a producer can
// check that its key works and which application it acts on.
func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	app, err := s.q.GetApplication(r.Context(), applicationIDFrom(r.Context()))
	if err != nil {
		// The key was just authenticated, and deleting an application
		// deletes its keys, so a missing row here is unexpected.
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(app))
}
