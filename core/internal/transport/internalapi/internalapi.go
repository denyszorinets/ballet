// Package internalapi is Core's API for Ballet's own services under
// /internal/v1/. Only service tokens (kind "service", audience "core") are
// accepted; each endpoint additionally checks a capability.
package internalapi

import (
	"encoding/json"
	"net/http"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Prefix of all internal routes.
const Prefix = "/internal/v1/"

// Router registers internal endpoints with capability checks.
type Router struct {
	mux *http.ServeMux
}

// Register mounts the internal API on mux and returns a router for
// endpoints added by other packages.
func Register(mux *http.ServeMux, v *runtoken.Verifier) *Router {
	r := &Router{mux: http.NewServeMux()}
	r.Handle("GET /internal/v1/whoami", "", func(w http.ResponseWriter, req *http.Request) {
		c, _ := runtoken.ClaimsFromContext(req.Context())
		WriteJSON(w, http.StatusOK, map[string]any{"subject": c.Subject, "capabilities": c.Capabilities})
	})
	mux.Handle(Prefix, runtoken.Middleware(v, "core")(serviceOnly(r.mux)))
	return r
}

// Handle registers h for pattern, requiring capability (if not empty).
func (r *Router) Handle(pattern, capability string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
		c, _ := runtoken.ClaimsFromContext(req.Context())
		if capability != "" && !c.Can(capability) {
			WriteError(w, http.StatusForbidden, "forbidden", "missing capability "+capability)
			return
		}
		h(w, req)
	})
}

func serviceOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := runtoken.ClaimsFromContext(r.Context())
		if !ok || c.Kind != runtoken.KindService {
			WriteError(w, http.StatusForbidden, "forbidden", "service token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSON writes v as JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the standard error body.
func WriteError(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, map[string]string{"error": code, "message": msg})
}
