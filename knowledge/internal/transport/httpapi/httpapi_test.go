package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/store"
	"github.com/denyszorinets/ballet/knowledge/internal/transport/httpapi"
)

type env struct {
	api   http.Handler
	issue func(organization string, caps ...string) string
}

// contract validates responses against Core's OpenAPI spec, which
// describes Knowledge's paths with the /api prefix (ADR-0022).
func contract(t *testing.T, next http.Handler) http.Handler {
	data, err := os.ReadFile("../../../../core/api/openapi.yaml")
	require.NoError(t, err)
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromData(data)
	require.NoError(t, err)
	router, err := gorillamux.NewRouter(spec)
	require.NoError(t, err)
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)

		apiReq := r.Clone(r.Context())
		apiReq.URL.Path = "/api" + r.URL.Path
		apiReq.Body = io.NopCloser(bytes.NewReader(body))
		route, params, err := router.FindRoute(apiReq)
		require.NoError(t, err, "path missing from Core's spec: %s", apiReq.URL.Path)
		in := &openapi3filter.RequestValidationInput{Request: apiReq, PathParams: params, Route: route, Options: opts}
		err = openapi3filter.ValidateResponse(r.Context(), &openapi3filter.ResponseValidationInput{
			RequestValidationInput: in, Status: rec.Code, Header: rec.Header(),
			Body: io.NopCloser(bytes.NewReader(rec.Body.Bytes())), Options: opts,
		})
		assert.NoError(t, err, "response %d of %s %s violates the spec", rec.Code, r.Method, r.URL.Path)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
}

func setup(t *testing.T) env {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "k.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	svc := &app.Service{Store: st, Searcher: st, Now: time.Now, NewID: func() string { return uuid.Must(uuid.NewV7()).String() }}
	mux := http.NewServeMux()
	httpapi.Register(mux, runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now), svc)
	issuer := runtoken.NewIssuer(ring, time.Now)
	return env{api: contract(t, mux), issue: func(organization string, caps ...string) string {
		raw, err := issuer.Issue(runtoken.Claims{
			Kind: runtoken.KindService, Subject: "service:core", Audience: []string{httpapi.Audience},
			Organization: organization, ActingFor: "user-bob", Capabilities: caps,
		}, time.Hour)
		require.NoError(t, err)
		return raw
	}}
}

func (e env) call(t *testing.T, tok, method, path, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.api.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

const base = "/v1/organizations/acme/knowledge/entries"

func TestKnowledgeAPI_EntryLifecycle(t *testing.T) {
	e := setup(t)
	rw := e.issue("acme", runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite)

	code, entry := e.call(t, rw, "POST", base, `{"kind":"decision","title":"Use SQLite","body":"Because it is simple.","projects":["WEB"],"items":["WEB-3"]}`)
	require.Equal(t, http.StatusCreated, code, entry)
	assert.Equal(t, "user-bob", entry["created_by"], "author is the human behind Core")
	id := entry["id"].(string)
	e.call(t, rw, "POST", base, `{"kind":"note","title":"Unrelated"}`)

	code, entry = e.call(t, rw, "PATCH", base+"/"+id, `{"version":1,"body":"Because it is simple and portable."}`)
	require.Equal(t, http.StatusOK, code, entry)
	assert.EqualValues(t, 2, entry["version"])
	code, _ = e.call(t, rw, "PATCH", base+"/"+id, `{"version":1,"title":"stale"}`)
	assert.Equal(t, http.StatusConflict, code)

	code, list := e.call(t, rw, "GET", base+"?kind=decision", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, list["items"], 1)
	_, list = e.call(t, rw, "GET", base+"?item=WEB-3", "")
	assert.Len(t, list["items"], 1)
	_, list = e.call(t, rw, "GET", base+"?project=APP", "")
	assert.Empty(t, list["items"])

	code, versions := e.call(t, rw, "GET", base+"/"+id+"/versions", "")
	require.Equal(t, http.StatusOK, code)
	vs := versions["items"].([]any)
	require.Len(t, vs, 2)
	assert.Equal(t, "Because it is simple.", vs[1].(map[string]any)["body"], "old version kept")

	code, _ = e.call(t, rw, "POST", base, `{"kind":"wiki","title":"x"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = e.call(t, rw, "GET", base+"/nope", "")
	assert.Equal(t, http.StatusNotFound, code)
}

func TestKnowledgeAPI_TokenScopeIsEnforced(t *testing.T) {
	e := setup(t)
	acme := e.issue("acme", runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite)
	_, entry := e.call(t, acme, "POST", base, `{"kind":"note","title":"Secret plan"}`)
	id := entry["id"].(string)

	globex := e.issue("globex", runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite)
	readOnly := e.issue("acme", runtoken.CapKnowledgeRead)
	unscoped := e.issue("", runtoken.CapKnowledgeRead)

	code, _ := e.call(t, globex, "GET", base, "")
	assert.Equal(t, http.StatusForbidden, code, "another organization's token")
	code, _ = e.call(t, globex, "GET", "/v1/organizations/globex/knowledge/entries/"+id, "")
	assert.Equal(t, http.StatusNotFound, code, "entry IDs do not leak across spaces")
	code, _ = e.call(t, readOnly, "POST", base, `{"kind":"note","title":"x"}`)
	assert.Equal(t, http.StatusForbidden, code)
	code, _ = e.call(t, unscoped, "GET", base, "")
	assert.Equal(t, http.StatusForbidden, code)
	code, _ = e.call(t, "", "GET", base, "")
	assert.Equal(t, http.StatusUnauthorized, code)
	code, _ = e.call(t, readOnly, "GET", base, "")
	assert.Equal(t, http.StatusOK, code)
}

func TestKnowledgeAPI_Search(t *testing.T) {
	e := setup(t)
	rw := e.issue("acme", runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite)
	e.call(t, rw, "POST", base, `{"kind":"decision","title":"Invoice export","body":"CSV files"}`)
	e.call(t, rw, "POST", base, `{"kind":"note","title":"Login"}`)

	code, out := e.call(t, rw, "GET", "/v1/organizations/acme/knowledge/search?q=csv+invoice", "")
	require.Equal(t, http.StatusOK, code, out)
	items := out["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "Invoice export", items[0].(map[string]any)["entry"].(map[string]any)["title"])

	globex := e.issue("globex", runtoken.CapKnowledgeRead)
	code, _ = e.call(t, globex, "GET", "/v1/organizations/acme/knowledge/search?q=csv", "")
	assert.Equal(t, http.StatusForbidden, code)
}
