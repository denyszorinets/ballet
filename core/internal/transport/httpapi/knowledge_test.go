package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// denyWrites allows everything except knowledge.write.
type denyWrites struct{}

func (denyWrites) Authorize(_ context.Context, _ auth.Identity, a app.Action, _ app.Scope) error {
	if a == app.ActKnowledgeWrite {
		return app.ErrForbidden
	}
	return nil
}

func TestKnowledgeProxy_ForwardsWithScopedTokenAfterAuthorization(t *testing.T) {
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	verifier := runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now)
	var mu sync.Mutex
	var seen []runtoken.Claims
	var paths []string
	knowledge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := verifier.Verify(r.Context(), r.Header.Get("Authorization")[len("Bearer "):], "knowledge")
		require.NoError(t, err)
		mu.Lock()
		seen, paths = append(seen, c), append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(knowledge.Close)

	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	ctx := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice"})
	_, err = (&app.Tenancy{Store: st, Authz: allow{}, Now: time.Now, NewID: store.NewID}).
		CreateCustomer(ctx, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	u, _ := url.Parse(knowledge.URL)
	mux := http.NewServeMux()
	httpapi.Register(mux, httpapi.Deps{
		Authenticate: testUser,
		Knowledge: &httpapi.KnowledgeProxy{
			URL: u, Access: &app.KnowledgeAccess{Tenancy: st, Authz: denyWrites{}},
			Tokens: runtoken.NewIssuer(ring, time.Now), Transport: &http.Transport{},
		},
	})
	api := contract(t, mux)

	code, body := call(t, api, "GET", "/api/v1/customers/acme/knowledge/entries?kind=note", "bob", "")
	require.Equal(t, http.StatusOK, code, body)
	require.Len(t, seen, 1)
	assert.Equal(t, "/v1/customers/acme/knowledge/entries", paths[0], "the /api prefix is stripped")
	assert.Equal(t, runtoken.KindService, seen[0].Kind)
	assert.Equal(t, "acme", seen[0].Customer)
	assert.Equal(t, "bob", seen[0].ActingFor)
	assert.Equal(t, []string{runtoken.CapKnowledgeRead}, seen[0].Capabilities, "reads get read only")
	assert.WithinDuration(t, time.Now().Add(5*time.Minute), seen[0].Expiry, time.Minute)

	code, body = call(t, api, "POST", "/api/v1/customers/acme/knowledge/entries", "bob", `{"kind":"note","title":"x"}`)
	assert.Equal(t, http.StatusForbidden, code, body)
	code, _ = call(t, api, "GET", "/api/v1/customers/nobody/knowledge/entries", "bob", "")
	assert.Equal(t, http.StatusNotFound, code)
	assert.Len(t, seen, 1, "denied requests never reach Knowledge")
}
