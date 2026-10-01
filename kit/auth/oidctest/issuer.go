// Package oidctest provides a fake OIDC issuer for tests: discovery document,
// JWKS endpoint and signed access tokens.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Issuer is a running fake OIDC provider.
type Issuer struct {
	URL string
	key *rsa.PrivateKey
	kid string
}

// NewIssuer starts a fake issuer that is stopped when the test ends.
func NewIssuer(t testing.TB) *Issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	iss := &Issuer{key: key, kid: "test-key"}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	iss.URL = srv.URL

	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                iss.URL,
			"jwks_uri":                              iss.URL + "/jwks",
			"authorization_endpoint":                iss.URL + "/auth",
			"token_endpoint":                        iss.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &key.PublicKey, KeyID: iss.kid, Algorithm: "RS256", Use: "sig",
		}}})
	})
	return iss
}

// Token returns a signed access token for subject with the given audience,
// valid for one hour, carrying extra claims.
func (i *Issuer) Token(t testing.TB, subject, audience string, extra map[string]any) string {
	t.Helper()
	now := time.Now()
	return i.Sign(t, jwt.Claims{
		Issuer:   i.URL,
		Subject:  subject,
		Audience: jwt.Audience{audience},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(time.Hour)),
	}, extra)
}

// Sign signs arbitrary registered and extra claims.
func (i *Issuer) Sign(t testing.TB, claims jwt.Claims, extra map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: i.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", i.kid),
	)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	b := jwt.Signed(signer).Claims(claims)
	if extra != nil {
		b = b.Claims(extra)
	}
	raw, err := b.Serialize()
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return raw
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
