package internalapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/internalapi"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func TestBudgetCheck_RefusesWorkOverBudget(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	boot, _ := rbac.ParseBootstrap("sub:alice")
	authz := &app.RBAC{Store: st, Bootstrap: []rbac.Binding{boot}}
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"sub": "alice"}})
	ten := &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	_, err = ten.CreateCustomer(admin, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	_, err = ten.CreateProject(admin, app.CreateProjectInput{CustomerKey: "acme", Key: "WEB", Name: "Web"})
	require.NoError(t, err)
	bs := &app.Budgets{Store: st, Tenancy: st, Authz: authz, Now: time.Now}
	_, err = bs.Set(admin, "acme", "WEB", 1000, 5000, 0)
	require.NoError(t, err)
	usage := &app.Usage{Store: st, Tenancy: st, Authz: authz}
	_, _, err = usage.Ingest(t.Context(), []app.UsageInput{{OccurredAt: time.Now(), Customer: "acme", Project: "WEB",
		Ticket: "WEB-1", Status: 200, InputTokens: 600, OutputTokens: 300, CacheWrite: 100, CacheRead: 99999}})
	require.NoError(t, err)

	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	mux := http.NewServeMux()
	internalapi.RegisterBudgets(internalapi.Register(mux, runtoken.NewRingVerifier(ring, time.Now)), bs)
	raw, err := runtoken.NewIssuer(ring, time.Now).Issue(runtoken.Claims{Kind: runtoken.KindService, Subject: "service:gateway",
		Audience: []string{"core"}, Capabilities: []string{runtoken.CapCredentialsRead}}, time.Hour)
	require.NoError(t, err)
	check := func(query string) (int, internalapi.BudgetVerdict) {
		req := httptest.NewRequest(http.MethodGet, "/internal/v1/budget/check?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var v internalapi.BudgetVerdict
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return rec.Code, v
	}

	code, v := check("customer=acme&project=WEB&ticket=WEB-1")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, v.Allowed, "1000 counted tokens (cache reads excluded) reach the ticket budget")
	assert.Contains(t, v.Reason, "budget of the ticket is used up: 1000 of 1000 tokens")
	_, v = check("customer=acme&project=WEB&ticket=WEB-2")
	assert.True(t, v.Allowed)
	_, v = check("customer=acme&project=WEB")
	assert.True(t, v.Allowed, "the project's daily budget has room")
	code, _ = check("customer=acme&project=NOPE")
	assert.Equal(t, http.StatusNotFound, code)

	status, err := bs.Get(admin, "acme", "WEB")
	require.NoError(t, err)
	assert.Equal(t, int64(1000), status.UsedToday)
	_, err = bs.Set(admin, "acme", "WEB", 1000, 5000, 0)
	assert.ErrorIs(t, err, app.ErrConflict, "stale version")
	_, err = bs.Set(admin, "acme", "", 0, 900, 0)
	require.NoError(t, err)
	_, v = check("customer=acme&project=WEB")
	assert.False(t, v.Allowed)
	assert.Contains(t, v.Reason, "daily budget of the customer")
}
