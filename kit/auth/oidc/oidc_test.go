package oidc_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/oidctest"
)

const audience = "ballet"

func newVerifier(t *testing.T, iss *oidctest.Issuer) *oidc.Verifier {
	t.Helper()
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{IssuerURL: iss.URL, Audience: audience})
	require.NoError(t, err)
	return v
}

func TestVerify_AcceptsValidTokenAndExposesClaims(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	v := newVerifier(t, iss)
	token := iss.Token(t, "user-1", audience, map[string]any{
		"email": "alice@example.com", "name": "Alice", "groups": []string{"ballet-admins"},
	})

	id, err := v.Verify(t.Context(), token)

	require.NoError(t, err)
	assert.Equal(t, "user-1", id.Subject)
	assert.Equal(t, "alice@example.com", id.Email)
	assert.Equal(t, "Alice", id.Name)
	assert.Equal(t, []any{"ballet-admins"}, id.Claims["groups"])
}

func TestVerify_RejectsInvalidTokens(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	other := oidctest.NewIssuer(t)
	v := newVerifier(t, iss)
	now := time.Now()

	tests := []struct {
		name  string
		token string
	}{
		{name: "wrong audience", token: iss.Token(t, "u", "someone-else", nil)},
		{name: "signed by another issuer", token: other.Token(t, "u", audience, nil)},
		{name: "expired", token: iss.Sign(t, jwt.Claims{
			Issuer: iss.URL, Subject: "u", Audience: jwt.Audience{audience},
			Expiry: jwt.NewNumericDate(now.Add(-time.Minute)),
		}, nil)},
		{name: "garbage", token: "not-a-jwt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.Verify(t.Context(), tt.token)
			assert.Error(t, err)
		})
	}
}

func TestNewVerifier_FailsForUnreachableIssuer(t *testing.T) {
	_, err := oidc.NewVerifier(t.Context(), oidc.Config{IssuerURL: "http://127.0.0.1:1", Audience: audience})

	assert.Error(t, err)
}

func TestMiddleware_RejectsMissingAndInvalidTokens(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	h := oidc.Middleware(newVerifier(t, iss))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler must not be called")
	}))

	for name, header := range map[string]string{"missing": "", "not bearer": "Basic abc", "invalid": "Bearer nope"} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}

			h.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, `Bearer realm="ballet"`, rec.Header().Get("WWW-Authenticate"))
			var body map[string]string
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, "unauthenticated", body["error"])
			assert.NotEmpty(t, body["message"])
		})
	}
}

func TestMiddleware_PutsIdentityInContext(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	var got auth.Identity
	h := oidc.Middleware(newVerifier(t, iss))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.FromContext(r.Context())
		require.True(t, ok)
		got = id
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+iss.Token(t, "user-1", audience, nil))

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "user-1", got.Subject)
	assert.Equal(t, auth.KindHuman, got.Kind)
}
