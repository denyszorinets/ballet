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
	"github.com/denyszorinets/ballet/core/internal/domain/credential"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/infra/secrets"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/internalapi"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func TestResolveCredential_ForGatewayOnly(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	box, err := secrets.LoadKey(filepath.Join(t.TempDir(), "secrets.key"))
	require.NoError(t, err)
	boot, _ := rbac.ParseBootstrap("sub:alice")
	authz := &app.RBAC{Store: st, Bootstrap: []rbac.Binding{boot}}
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"sub": "alice"}})
	ten := &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	_, err = ten.CreateCustomer(admin, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	cr := &app.Credentials{Store: st, Tenancy: st, Authz: authz, Box: box, Now: time.Now, NewID: store.NewID}
	_, err = cr.Set(admin, app.SetCredentialInput{CustomerKey: "acme", Provider: credential.ProviderAnthropic, APIKey: "sk-ant-x"})
	require.NoError(t, err)

	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	mux := http.NewServeMux()
	internalapi.RegisterCredentials(internalapi.Register(mux, runtoken.NewRingVerifier(ring, time.Now)), cr)
	tok := func(caps ...string) string {
		raw, err := runtoken.NewIssuer(ring, time.Now).Issue(runtoken.Claims{
			Kind: runtoken.KindService, Subject: "service:x", Audience: []string{"core"}, Capabilities: caps,
		}, time.Hour)
		require.NoError(t, err)
		return raw
	}
	get := func(query, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/internal/v1/credentials/resolve?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := get("customer=acme&provider=anthropic", tok(runtoken.CapCredentialsRead))
	require.Equal(t, http.StatusOK, rec.Code)
	var got internalapi.ResolvedCredential
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "sk-ant-x", got.APIKey)

	assert.Equal(t, http.StatusForbidden, get("customer=acme&provider=anthropic", tok(runtoken.CapRunnerConnect)).Code)
	assert.Equal(t, http.StatusNotFound, get("customer=acme&provider=openai", tok(runtoken.CapCredentialsRead)).Code)
	assert.Equal(t, http.StatusBadRequest, get("customer=acme&provider=git", tok(runtoken.CapCredentialsRead)).Code,
		"git tokens are not served to services")
}
