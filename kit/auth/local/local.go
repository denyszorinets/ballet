// Package local is authentication for a single-user installation: when no
// identity provider is configured, every caller is the same local user.
package local

import (
	"context"
	"net/http"

	"github.com/denyszorinets/ballet/kit/auth"
)

// Subject identifies the local user (and its role bindings: "sub:local").
const Subject = "local"

// Identity is the local user.
func Identity() auth.Identity {
	return auth.Identity{Kind: auth.KindHuman, Subject: Subject, Name: "Local user",
		Claims: map[string]any{"sub": Subject, "name": "Local user"}}
}

// Authenticator accepts every caller as the local user.
type Authenticator struct{}

// Verify ignores the token and returns the local user.
func (Authenticator) Verify(context.Context, string) (auth.Identity, error) { return Identity(), nil }

// Middleware puts the local user into every request's context.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), Identity())))
	})
}
