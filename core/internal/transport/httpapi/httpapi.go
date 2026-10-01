// Package httpapi exposes Core's REST API under /api/.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Deps are the collaborators the REST API needs.
type Deps struct {
	// Authenticate rejects unauthenticated requests and puts the caller's
	// identity into the request context (oidc.Middleware in production).
	Authenticate func(http.Handler) http.Handler
	TokenKeys    *runtoken.KeyRing
	Tenancy      *app.Tenancy
	RBAC         *app.RBAC
	RoleBindings *app.RoleBindings
	Tracker      *app.Tracker
}

// Register mounts the REST API on mux. Every /api/ route requires an
// authenticated caller. The run token JWKS is public.
func Register(mux *http.ServeMux, d Deps) {
	mux.Handle("GET /.well-known/jwks.json", runtoken.JWKSHandler(d.TokenKeys))

	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/me", me(d.RBAC))
	registerTenancy(api, d.Tenancy)
	registerRBAC(api, d.RoleBindings)
	registerTracker(api, d.Tracker)
	api.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, fmt.Errorf("%w: no such endpoint", app.ErrNotFound))
	})
	mux.Handle("/api/", d.Authenticate(api))
}

type meResponse struct {
	Subject  string        `json:"subject"`
	Email    string        `json:"email"`
	Name     string        `json:"name"`
	Groups   []string      `json:"groups"`
	Bindings []bindingJSON `json:"bindings"`
}

// me returns the caller and the role bindings that apply to them.
func me(r *app.RBAC) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, _ := auth.FromContext(req.Context()) // guaranteed by the middleware
		bindings, err := r.Effective(req.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		out := meResponse{
			Subject: id.Subject, Email: id.Email, Name: id.Name,
			Groups: stringSlice(id.Claims["groups"]), Bindings: []bindingJSON{},
		}
		for _, b := range bindings {
			out.Bindings = append(out.Bindings, toBindingJSON(b))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// stringSlice converts a JSON array claim into strings, skipping non-strings.
func stringSlice(v any) []string {
	out := []string{}
	items, _ := v.([]any)
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody is the JSON body of every error response.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeError maps app errors to HTTP status codes and error codes.
func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, app.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_argument"
	case errors.Is(err, app.ErrUnauthorized):
		status, code = http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, app.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, app.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, app.ErrAlreadyExists):
		status, code = http.StatusConflict, "already_exists"
	case errors.Is(err, app.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		slog.Error("request failed", "error", err)
		msg = "internal error"
	}
	writeJSON(w, status, errorBody{Error: code, Message: msg})
}

// maxBody limits request bodies.
const maxBody = 1 << 20

// decode reads a JSON body into dst, rejecting unknown fields.
func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: request body: %w", app.ErrInvalid, err)
	}
	return nil
}
