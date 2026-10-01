package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/oidctest"
)

func newAPI(t *testing.T) (http.Handler, *oidctest.Issuer) {
	t.Helper()
	iss := oidctest.NewIssuer(t)
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{IssuerURL: iss.URL, Audience: "ballet"})
	require.NoError(t, err)
	mux := http.NewServeMux()
	httpapi.Register(mux, httpapi.Deps{Verifier: v})
	return mux, iss
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
