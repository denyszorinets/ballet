package knowledge_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/infra/knowledge"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

type tenants struct{ app.TenancyStore }

func (tenants) OrganizationByKey(_ context.Context, key string) (tenancy.Organization, error) {
	return tenancy.Organization{ID: "c1", Key: key}, nil
}

// readers may read acme's knowledge; writers may also write.
type authz struct{ write bool }

func (a authz) Authorize(_ context.Context, _ auth.Identity, action app.Action, s app.Scope) error {
	if s.Organization == "acme" && (action == app.ActKnowledgeRead || a.write) {
		return nil
	}
	return app.ErrForbidden
}

func TestClient_ForwardsWithScopedToken(t *testing.T) {
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	verifier := runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now)
	var claims runtoken.Claims
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err = verifier.Verify(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "knowledge")
		require.NoError(t, err)
		data, _ := io.ReadAll(r.Body)
		body = nil
		_ = json.Unmarshal(data, &body)
		switch {
		case r.URL.Path == "/v1/organizations/acme/knowledge/search" && r.URL.Query().Get("q") == "auth":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.URL.Path == "/v1/organizations/acme/knowledge/entries" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"k1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found","message":"entry not found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	client := func(write bool) *knowledge.Client {
		return &knowledge.Client{URL: u, Access: &app.KnowledgeAccess{Tenancy: tenants{}, Authz: authz{write}}, Tokens: runtoken.NewIssuer(ring, time.Now)}
	}
	bob := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "bob"})

	out, err := client(false).Do(bob, "acme", false, http.MethodGet, "search", url.Values{"q": {"auth"}}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"items":[]}`, string(out))
	assert.Equal(t, []string{runtoken.CapKnowledgeRead}, claims.Capabilities)
	assert.Equal(t, "bob", claims.ActingFor)
	assert.Equal(t, "service:core", claims.Subject)

	_, err = client(false).Do(bob, "acme", true, http.MethodPost, "entries", nil, map[string]any{"title": "x"})
	assert.ErrorIs(t, err, app.ErrForbidden, "a reader cannot write")

	planner := app.ActingAsPlanner(bob, "s1")
	out, err = client(true).Do(planner, "acme", true, http.MethodPost, "entries", nil, map[string]any{"title": "x"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"k1"}`, string(out))
	assert.Equal(t, "x", body["title"])
	assert.Equal(t, "planner:s1", claims.Subject)
	assert.Contains(t, claims.Capabilities, runtoken.CapKnowledgeWrite)

	_, err = client(true).Do(bob, "acme", false, http.MethodGet, "entries/nope", nil, nil)
	assert.ErrorIs(t, err, app.ErrNotFound)
	assert.ErrorContains(t, err, "entry not found")
	_, err = client(true).Do(bob, "globex", false, http.MethodGet, "entries", nil, nil)
	assert.ErrorIs(t, err, app.ErrForbidden)
}
