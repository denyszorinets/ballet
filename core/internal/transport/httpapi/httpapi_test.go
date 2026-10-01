package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/infra/secrets"
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthenticated","message":"no test user"}`))
			return
		}
		id := auth.Identity{Kind: auth.KindHuman, Subject: sub, Claims: map[string]any{"sub": sub, "groups": []any{"g1"}}}
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
	box, err := secrets.LoadKey(filepath.Join(t.TempDir(), "secrets.key"))
	require.NoError(t, err)
	admin, err := rbac.ParseBootstrap("sub:alice")
	require.NoError(t, err)
	r := &app.RBAC{Store: st, Bootstrap: []rbac.Binding{admin}}
	mux := http.NewServeMux()
	httpapi.Register(mux, httpapi.Deps{
		Authenticate: authn,
		TokenKeys:    ring,
		Tenancy:      &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID},
		RBAC:         r,
		RoleBindings: &app.RoleBindings{RBAC: r, Tenancy: st, Now: time.Now, NewID: store.NewID},
		Usage:        &app.Usage{Store: st, Tenancy: st, Authz: authz},
		Skills:       &app.Skills{Store: st, Tenancy: st, Authz: authz, Now: time.Now, NewID: store.NewID},
		Search:       &app.Search{Store: st, Tenancy: st, Authz: authz},
		Credentials:  &app.Credentials{Store: st, Tenancy: st, Authz: authz, Box: box, Now: time.Now, NewID: store.NewID},
		Tracker:      &app.Tracker{Items: st, Deps: st, Tenancy: st, Events: st, Authz: authz, Now: time.Now, NewID: store.NewID},
	})
	return contract(t, mux)
}

func call(t *testing.T, h http.Handler, method, path, user, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
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
		"groups": []any{"ballet-admins"}, "bindings": []any{},
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

func TestRBACAPI_RolesAndBindings(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)

	code, roles := call(t, api, "GET", "/api/v1/roles", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, roles["items"], 5)

	code, b := call(t, api, "POST", "/api/v1/role-bindings", "alice",
		`{"claim":"groups","value":"g1","role":"viewer","scope":"customer:acme"}`)
	require.Equal(t, http.StatusCreated, code, b)
	assert.Equal(t, "customer:acme", b["scope"])

	// /me shows the binding: the test user's groups claim contains g1.
	code, me := call(t, api, "GET", "/api/v1/me", "bob", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, me["bindings"], 1)

	code, list := call(t, api, "GET", "/api/v1/role-bindings", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, list["items"], 2, "bootstrap + stored")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/role-bindings/"+b["id"].(string), nil)
	req.Header.Set("X-Test-User", "alice")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	code, body := call(t, api, "POST", "/api/v1/role-bindings", "alice",
		`{"claim":"groups","value":"g1","role":"viewer","scope":"customer:nobody"}`)
	assert.Equal(t, http.StatusNotFound, code, body)
}

func TestTrackerAPI_ItemLifecycle(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)
	call(t, api, "POST", "/api/v1/customers/acme/projects", "alice", `{"key":"WEB","name":"Web","description":""}`)

	code, epic := call(t, api, "POST", "/api/v1/projects/WEB/items", "alice", `{"kind":"epic","title":"Billing"}`)
	require.Equal(t, http.StatusCreated, code, epic)
	assert.Equal(t, "WEB-1", epic["key"])
	assert.NotContains(t, epic, "policy", "only tickets have a policy")

	code, tk := call(t, api, "POST", "/api/v1/projects/WEB/items", "alice",
		`{"kind":"ticket","title":"Export","type":"feature","acceptance_criteria":["CSV"],"epic":"WEB-1"}`)
	require.Equal(t, http.StatusCreated, code, tk)
	assert.Equal(t, "WEB-2", tk["key"])
	assert.Equal(t, "backlog", tk["state"])
	assert.Equal(t, "WEB-1", tk["epic"])
	assert.Equal(t, map[string]any{"review_mode": "agent", "merge_mode": "auto"}, tk["policy"])

	code, tk = call(t, api, "PATCH", "/api/v1/items/WEB-2", "alice",
		`{"version":1,"policy":{"review_mode":"agent+human","merge_mode":"manual"},"epic":""}`)
	require.Equal(t, http.StatusOK, code, tk)
	assert.NotContains(t, tk, "epic", "epic removed")

	code, tk = call(t, api, "POST", "/api/v1/items/WEB-2/transition", "alice", `{"state":"ready","version":2}`)
	require.Equal(t, http.StatusOK, code, tk)
	assert.Equal(t, "ready", tk["state"])

	code, body := call(t, api, "POST", "/api/v1/items/WEB-2/transition", "alice", `{"state":"done","version":2}`)
	assert.Equal(t, http.StatusConflict, code, body)

	code, list := call(t, api, "GET", "/api/v1/projects/WEB/items?kind=ticket", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, list["items"], 1)

	code, hist := call(t, api, "GET", "/api/v1/items/WEB-2/history", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, hist["items"], 3)

	code, _ = call(t, api, "GET", "/api/v1/items/WEB-99", "alice", "")
	assert.Equal(t, http.StatusNotFound, code)
}

func TestDependencyAPI(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)
	call(t, api, "POST", "/api/v1/customers/acme/projects", "alice", `{"key":"WEB","name":"Web","description":""}`)
	for range 2 {
		call(t, api, "POST", "/api/v1/projects/WEB/items", "alice", `{"kind":"ticket","title":"t"}`)
	}
	for _, k := range []string{"WEB-1", "WEB-2"} {
		call(t, api, "POST", "/api/v1/items/"+k+"/transition", "alice", `{"state":"ready","version":1}`)
	}

	code, d := call(t, api, "POST", "/api/v1/items/WEB-2/dependencies", "alice", `{"type":"blocked_by","item":"WEB-1"}`)
	require.Equal(t, http.StatusCreated, code, d)
	assert.Equal(t, "blocked_by", d["type"])
	assert.Equal(t, "WEB-1", d["item"].(map[string]any)["key"])

	code, body := call(t, api, "POST", "/api/v1/items/WEB-1/dependencies", "alice", `{"type":"blocked_by","item":"WEB-2"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["message"], "cycle")

	code, run := call(t, api, "GET", "/api/v1/projects/WEB/runnable", "alice", "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, run["items"], 1)
	assert.Equal(t, "WEB-1", run["items"].([]any)[0].(map[string]any)["key"])

	code, list := call(t, api, "GET", "/api/v1/items/WEB-1/dependencies", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "blocks", list["items"].([]any)[0].(map[string]any)["type"])

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dependencies/"+d["id"].(string), nil)
	req.Header.Set("X-Test-User", "alice")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	_, run = call(t, api, "GET", "/api/v1/projects/WEB/runnable", "alice", "")
	assert.Len(t, run["items"], 2)
}

func TestCredentialAPI_SetListDeleteWithoutExposingSecrets(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)
	call(t, api, "POST", "/api/v1/customers/acme/projects", "alice", `{"key":"WEB","name":"Web","description":""}`)

	code, c := call(t, api, "PUT", "/api/v1/customers/acme/credentials/anthropic", "alice", `{"api_key":"sk-ant-secret-1234"}`)
	require.Equal(t, http.StatusOK, code, c)
	assert.NotContains(t, fmt.Sprint(c), "secret")
	assert.Contains(t, c["fingerprint"], "1234")

	code, c = call(t, api, "PUT", "/api/v1/projects/WEB/credentials/anthropic", "alice", `{"api_key":"sk-web","base_url":"https://llm.example"}`)
	require.Equal(t, http.StatusOK, code, c)
	assert.Equal(t, "WEB", c["project"])

	code, list := call(t, api, "GET", "/api/v1/customers/acme/credentials", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, list["items"], 2)
	assert.NotContains(t, fmt.Sprint(list), "sk-")

	code, body := call(t, api, "PUT", "/api/v1/customers/acme/credentials/gemini", "alice", `{"api_key":"x"}`)
	assert.Equal(t, http.StatusBadRequest, code, body)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/WEB/credentials/anthropic", nil)
	req.Header.Set("X-Test-User", "alice")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	_, list = call(t, api, "GET", "/api/v1/customers/acme/credentials", "alice", "")
	assert.Len(t, list["items"], 1)
}

func TestUsageAPI_ReportShape(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)
	call(t, api, "POST", "/api/v1/customers/acme/projects", "alice", `{"key":"WEB","name":"Web","description":""}`)

	code, rep := call(t, api, "GET", "/api/v1/projects/WEB/usage?group_by=model", "alice", "")
	require.Equal(t, http.StatusOK, code, rep)
	assert.Equal(t, "model", rep["group_by"])
	assert.Empty(t, rep["items"])

	code, _ = call(t, api, "GET", "/api/v1/projects/WEB/usage?since=yesterday", "alice", "")
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestSkillAPI_Lifecycle(t *testing.T) {
	api := newAPI(t, testUser, allow{})

	code, s := call(t, api, "POST", "/api/v1/skills", "alice",
		`{"scope":"organization","name":"gitflow","description":"Branching rules","body":"# v1","files":{"scripts/x.sh":"echo"}}`)
	require.Equal(t, http.StatusCreated, code, s)
	id := s["id"].(string)
	assert.EqualValues(t, 0, s["latest_version"])

	code, v := call(t, api, "POST", "/api/v1/skills/"+id+"/publish", "alice", `{"version":1}`)
	require.Equal(t, http.StatusCreated, code, v)
	assert.EqualValues(t, 1, v["number"])

	code, s = call(t, api, "PATCH", "/api/v1/skills/"+id, "alice", `{"version":2,"body":"# v2"}`)
	require.Equal(t, http.StatusOK, code, s)
	code, v = call(t, api, "GET", "/api/v1/skills/"+id+"/versions/1", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "# v1", v["body"])

	code, list := call(t, api, "GET", "/api/v1/skills?scope=organization", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, list["items"], 1)
	code, _ = call(t, api, "GET", "/api/v1/skills", "alice", "")
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = call(t, api, "GET", "/api/v1/skills/"+id+"/versions/x", "alice", "")
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = call(t, api, "GET", "/api/v1/skills/"+id+"/versions/9", "alice", "")
	assert.Equal(t, http.StatusNotFound, code)
}

func TestSkillAPI_ResolutionAndPins(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	call(t, api, "POST", "/api/v1/customers", "alice", `{"key":"acme","name":"Acme"}`)
	call(t, api, "POST", "/api/v1/customers/acme/projects", "alice", `{"key":"WEB","name":"Web","description":""}`)
	_, s := call(t, api, "POST", "/api/v1/skills", "alice", `{"scope":"organization","name":"gitflow","description":"d"}`)
	call(t, api, "POST", "/api/v1/skills/"+s["id"].(string)+"/publish", "alice", `{"version":1}`)

	code, list := call(t, api, "GET", "/api/v1/projects/WEB/skills", "alice", "")
	require.Equal(t, http.StatusOK, code, list)
	require.Len(t, list["items"], 1)
	assert.Equal(t, "organization", list["items"].([]any)[0].(map[string]any)["scope"])

	code, body := call(t, api, "PUT", "/api/v1/projects/WEB/skills/gitflow/pin", "alice", `{"version":5}`)
	require.Equal(t, http.StatusNoContent, code, body)
	_, list = call(t, api, "GET", "/api/v1/projects/WEB/skills", "alice", "")
	assert.NotEmpty(t, list["items"].([]any)[0].(map[string]any)["problem"])
	code, pins := call(t, api, "GET", "/api/v1/projects/WEB/skill-pins", "alice", "")
	require.Equal(t, http.StatusOK, code, pins)
	assert.Equal(t, []any{map[string]any{"name": "gitflow", "version": 5.0, "disabled": false}}, pins["items"])

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/WEB/skills/gitflow/pin", nil)
	req.Header.Set("X-Test-User", "alice")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestSearchAPI_Shape(t *testing.T) {
	api := newAPI(t, testUser, allow{})

	code, out := call(t, api, "GET", "/api/v1/search?q=anything", "alice", "")
	require.Equal(t, http.StatusOK, code, out)
	assert.Empty(t, out["items"])
	code, _ = call(t, api, "GET", "/api/v1/search?q=x&kind=user", "alice", "")
	assert.Equal(t, http.StatusBadRequest, code)
}
