package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/oidctest"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

type allow struct{}

func (allow) Authorize(context.Context, auth.Identity, app.Action, app.Scope) error { return nil }

// testUser authenticates every request as the subject in the X-Test-User
// header (401 without it).
func testUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := r.Header.Get("X-Test-User")
		if sub == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id := auth.Identity{Kind: auth.KindHuman, Subject: sub, Claims: map[string]any{"groups": []any{"g1"}}}
		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
	})
}

func newAPI(t *testing.T, authn func(http.Handler) http.Handler, authz app.Authorizer) http.Handler {
	t.Helper()
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	mux := http.NewServeMux()
	httpapi.Register(mux, httpapi.Deps{
		Authenticate: authn,
		TokenKeys:    ring,
		Tenancy:      &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID},
	})
	return mux
}

func call(t *testing.T, h http.Handler, method, path, user, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	}
	return rec.Code, out
}

func TestMe_WithRealOIDCVerifier(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{IssuerURL: iss.URL, Audience: "ballet"})
	require.NoError(t, err)
	api := newAPI(t, oidc.Middleware(v), allow{})
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

	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestJWKS_IsPublicAndListsSigningKeys(t *testing.T) {
	api := newAPI(t, testUser, allow{})
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

func TestTenancyAPI_CustomerAndProjectLifecycle(t *testing.T) {
	api := newAPI(t, testUser, allow{})

	code, c := call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)
	require.Equal(t, http.StatusCreated, code, c)
	assert.Equal(t, "acme", c["key"])
	assert.EqualValues(t, 1, c["version"])

	code, p := call(t, api, "POST", "/api/v1/customers/acme/projects", "alice",
		`{"key":"ACME","name":"Acme Shop","description":"Online shop"}`)
	require.Equal(t, http.StatusCreated, code, p)
	assert.Equal(t, "acme", p["customer"])

	code, list := call(t, api, "GET", "/api/v1/customers/acme/projects", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, list["items"], 1)

	code, p = call(t, api, "PATCH", "/api/v1/projects/ACME", "alice",
		`{"name":"Acme Store","description":"Online store","version":1}`)
	require.Equal(t, http.StatusOK, code, p)
	assert.Equal(t, "Acme Store", p["name"])
	assert.EqualValues(t, 2, p["version"])

	code, body := call(t, api, "PATCH", "/api/v1/projects/ACME", "alice",
		`{"name":"Stale","description":"","version":1}`)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "conflict", body["error"])

	code, cs := call(t, api, "GET", "/api/v1/customers", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, cs["items"], 1)
}

func TestTenancyAPI_ErrorMapping(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)

	tests := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"duplicate", "POST", "/api/v1/customers", `{"key":"acme","name":"Again"}`, 409, "already_exists"},
		{"invalid key", "POST", "/api/v1/customers", `{"key":"A B","name":"X"}`, 400, "invalid_argument"},
		{"unknown field", "POST", "/api/v1/customers", `{"key":"x1","name":"X","extra":1}`, 400, "invalid_argument"},
		{"malformed json", "POST", "/api/v1/customers", `{`, 400, "invalid_argument"},
		{"missing customer", "GET", "/api/v1/customers/nobody", "", 404, "not_found"},
		{"missing project", "GET", "/api/v1/projects/NOPE", "", 404, "not_found"},
		{"unknown endpoint", "GET", "/api/v1/nothing", "", 404, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := call(t, api, tt.method, tt.path, "alice", tt.body)
			assert.Equal(t, tt.status, code)
			assert.Equal(t, tt.code, body["error"])
			assert.NotEmpty(t, body["message"])
		})
	}
}

func TestTenancyAPI_DenyAllReturnsForbidden(t *testing.T) {
	api := newAPI(t, testUser, app.DenyAll{})

	code, body := call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)

	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "forbidden", body["error"])

	code, list := call(t, api, "GET", "/api/v1/customers", "alice", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, list["items"], "lists only show what the caller may read")
}

func TestTenancyAPI_RequiresAuthentication(t *testing.T) {
	api := newAPI(t, testUser, allow{})

	code, _ := call(t, api, "GET", "/api/v1/customers", "", "")

	assert.Equal(t, http.StatusUnauthorized, code)
}
