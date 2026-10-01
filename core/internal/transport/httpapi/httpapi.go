// Package httpapi exposes Core's REST API under /api/.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Deps are the collaborators the REST API needs.
type Deps struct {
	Verifier  *oidc.Verifier
	TokenKeys *runtoken.KeyRing
}

// Register mounts the REST API on mux. Every /api/ route requires an
// authenticated caller. The run token JWKS is public.
func Register(mux *http.ServeMux, d Deps) {
	mux.Handle("GET /.well-known/jwks.json", runtoken.JWKSHandler(d.TokenKeys))

	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/me", me)
	mux.Handle("/api/", oidc.Middleware(d.Verifier)(api))
}

type meResponse struct {
	Subject string   `json:"subject"`
	Email   string   `json:"email"`
	Name    string   `json:"name"`
	Groups  []string `json:"groups"`
}

func me(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.FromContext(r.Context()) // guaranteed by the middleware
	writeJSON(w, http.StatusOK, meResponse{
		Subject: id.Subject,
		Email:   id.Email,
		Name:    id.Name,
		Groups:  stringSlice(id.Claims["groups"]),
	})
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
