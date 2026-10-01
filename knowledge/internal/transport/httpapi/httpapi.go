// Package httpapi is Knowledge's REST API under /v1/customers/{customer}/
// knowledge/ — the paths of Core's /api/... without the /api prefix
// (ADR-0022). Requests need a Core-issued token for audience "knowledge".
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/domain"
)

// Audience of tokens accepted by Knowledge.
const Audience = "knowledge"

type entryJSON struct {
	ID        string      `json:"id"`
	Kind      domain.Kind `json:"kind"`
	Title     string      `json:"title"`
	Body      string      `json:"body"`
	Projects  []string    `json:"projects"`
	Items     []string    `json:"items"`
	Version   int64       `json:"version"`
	CreatedBy string      `json:"created_by"`
	UpdatedBy string      `json:"updated_by"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

func toJSON(e domain.Entry) entryJSON {
	nz := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	return entryJSON{ID: e.ID, Kind: e.Kind, Title: e.Title, Body: e.Body, Projects: nz(e.Projects), Items: nz(e.Items),
		Version: e.Version, CreatedBy: e.CreatedBy, UpdatedBy: e.UpdatedBy, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt}
}

type versionJSON struct {
	Version   int64     `json:"version"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

// Register mounts the API on mux.
func Register(mux *http.ServeMux, v *runtoken.Verifier, s *app.Service) {
	api := http.NewServeMux()
	const base = "/v1/customers/{customer}/knowledge/entries"

	api.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		list, err := s.List(r.Context(), r.PathValue("customer"), app.Filter{
			Kind: domain.Kind(q.Get("kind")), Project: q.Get("project"), Item: q.Get("item"),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		out := struct {
			Items []entryJSON `json:"items"`
		}{Items: make([]entryJSON, 0, len(list))}
		for _, e := range list {
			out.Items = append(out.Items, toJSON(e))
		}
		writeJSON(w, http.StatusOK, out)
	})

	api.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kind     domain.Kind `json:"kind"`
			Title    string      `json:"title"`
			Body     string      `json:"body"`
			Projects []string    `json:"projects"`
			Items    []string    `json:"items"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		e, err := s.Create(r.Context(), r.PathValue("customer"), app.CreateInput{
			Kind: in.Kind, Title: in.Title, Body: in.Body, Projects: in.Projects, Items: in.Items,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toJSON(e))
	})

	api.HandleFunc("GET "+base+"/{entry}", func(w http.ResponseWriter, r *http.Request) {
		e, err := s.Get(r.Context(), r.PathValue("customer"), r.PathValue("entry"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toJSON(e))
	})

	api.HandleFunc("PATCH "+base+"/{entry}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version  int64        `json:"version"`
			Kind     *domain.Kind `json:"kind"`
			Title    *string      `json:"title"`
			Body     *string      `json:"body"`
			Projects *[]string    `json:"projects"`
			Items    *[]string    `json:"items"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		e, err := s.Update(r.Context(), r.PathValue("customer"), r.PathValue("entry"), app.UpdateInput{
			Version: in.Version, Kind: in.Kind, Title: in.Title, Body: in.Body, Projects: in.Projects, Items: in.Items,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toJSON(e))
	})

	api.HandleFunc("GET "+base+"/{entry}/versions", func(w http.ResponseWriter, r *http.Request) {
		vs, err := s.Versions(r.Context(), r.PathValue("customer"), r.PathValue("entry"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := struct {
			Items []versionJSON `json:"items"`
		}{Items: make([]versionJSON, 0, len(vs))}
		for _, v := range vs {
			out.Items = append(out.Items, versionJSON{Version: v.Version, Title: v.Title, Body: v.Body, Author: v.Author, CreatedAt: v.CreatedAt})
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.Handle("/v1/", runtoken.Middleware(v, Audience)(api))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, app.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_argument"
	case errors.Is(err, app.ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, app.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, app.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, app.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		slog.Error("request failed", "error", err)
		msg = "internal error"
	}
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}

func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: request body: %w", app.ErrInvalid, err)
	}
	return nil
}
