package runtoken

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/denyszorinets/ballet/kit/auth"
)

type claimsKey struct{}

// ClaimsFromContext returns the run token claims stored by Middleware.
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(Claims)
	return c, ok
}

// Middleware requires a valid run token for audience. It stores the claims
// and a service Identity in the request context.
func Middleware(v *Verifier, audience string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || raw == "" {
				unauthorized(w)
				return
			}
			c, err := v.Verify(r.Context(), raw, audience)
			if err != nil {
				slog.DebugContext(r.Context(), "rejected run token", "error", err)
				unauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey{}, c)
			ctx = auth.WithIdentity(ctx, auth.Identity{Kind: auth.KindService, Subject: c.Subject})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="ballet"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthenticated"})
}
