package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/oidctest"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func newAPI(t *testing.T) (http.Handler, *oidctest.Issuer) {
	t.Helper()
	iss := oidctest.NewIssuer(t)
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{IssuerURL: iss.URL, Audience: "ballet"})
	require.NoError(t, err)
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	mux := http.NewServeMux()
	httpapi.Register(mux, httpapi.Deps{Verifier: v, TokenKeys: ring})
	return mux, iss
}

func TestJWKS_IsPublicAndListsSigningKeys(t *testing.T) {
	api, _ := newAPI(t)
	rec := httptest.NewRecorder()

	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Keys, 1)
	assert.Equal(t, "OKP", body.Keys[0]["kty"])
	assert.Equal(t, "EdDSA", body.Keys[0]["alg"])
	assert.NotContains(t, body.Keys[0], "d", "private key material must not be published")
}

func TestMe_ReturnsAuthenticatedIdentity(t *testing.T) {
	api, iss := newAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+iss.Token(t, "user-1", "ballet", map[string]any{
		"email": "alice@example.com", "name": "Alice", "groups": []string{"ballet-admins"},
	}))
	rec := httptest.NewRecorder()

	api.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{
		"subject": "user-1", "email": "alice@example.com", "name": "Alice",
		"groups": []any{"ballet-admins"},
	}, body)
}

func TestAPI_RequiresAuthentication(t *testing.T) {
	api, _ := newAPI(t)
	rec := httptest.NewRecorder()

	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
