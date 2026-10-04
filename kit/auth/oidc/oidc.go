// Package oidc authenticates humans with OIDC access tokens (ADR-0006).
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"github.com/denyszorinets/ballet/kit/auth"
)

// Config identifies the trusted issuer and the audience tokens must carry.
type Config struct {
	IssuerURL string `toml:"issuer_url"`
	Audience  string `toml:"audience"`
}

// Enabled reports whether an issuer is configured; without one, services
// run without authentication (kit/auth/local).
func (c Config) Enabled() bool { return c.IssuerURL != "" }

// Validate checks the settings of an enabled configuration.
func (c Config) Validate() error {
	var errs []error
	if c.Enabled() && c.Audience == "" {
		errs = append(errs, errors.New("oidc.audience must not be empty"))
	}
	return errors.Join(errs...)
}

// Verifier validates access tokens: signature (JWKS from discovery, cached
// and refreshed on unknown key IDs), issuer, audience and expiry.
type Verifier struct {
	v *gooidc.IDTokenVerifier
}

// NewVerifier discovers the issuer's configuration. It fails when the issuer
// is unreachable.
func NewVerifier(ctx context.Context, cfg Config) (*Verifier, error) {
	provider, err := gooidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer %s: %w", cfg.IssuerURL, err)
	}
	return &Verifier{v: provider.Verifier(&gooidc.Config{ClientID: cfg.Audience})}, nil
}

// Verify validates raw and returns the caller's identity.
func (v *Verifier) Verify(ctx context.Context, raw string) (auth.Identity, error) {
	tok, err := v.v.Verify(ctx, raw)
	if err != nil {
		return auth.Identity{}, fmt.Errorf("verify access token: %w", err)
	}
	claims := map[string]any{}
	if err := tok.Claims(&claims); err != nil {
		return auth.Identity{}, fmt.Errorf("decode token claims: %w", err)
	}
	email, _ := claims["email"].(string)
	name, _ := claims["name"].(string)
	return auth.Identity{
		Kind:    auth.KindHuman,
		Subject: tok.Subject,
		Email:   email,
		Name:    name,
		Claims:  claims,
	}, nil
}

// Middleware rejects requests without a valid "Authorization: Bearer" token
// with 401 and stores the identity in the request context otherwise.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || raw == "" {
				unauthorized(w)
				return
			}
			id, err := v.Verify(r.Context(), raw)
			if err != nil {
				slog.DebugContext(r.Context(), "rejected access token", "error", err)
				unauthorized(w)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="ballet"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "unauthenticated",
		"message": "a valid bearer token is required",
	})
}
