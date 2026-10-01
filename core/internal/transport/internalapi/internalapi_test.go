package internalapi_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/transport/internalapi"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func TestInternalAPI_AcceptsOnlyServiceTokensWithCapability(t *testing.T) {
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	iss := runtoken.NewIssuer(ring, time.Now)
	mux := http.NewServeMux()
	r := internalapi.Register(mux, runtoken.NewRingVerifier(ring, time.Now))
	r.Handle("GET /internal/v1/secret", runtoken.CapCredentialsRead, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	issue := func(c runtoken.Claims) string {
		raw, err := iss.Issue(c, time.Hour)
		require.NoError(t, err)
		return raw
	}
	gateway := issue(runtoken.Claims{Kind: runtoken.KindService, Subject: "service:gateway", Audience: []string{"core"},
		Capabilities: []string{runtoken.CapCredentialsRead}})
	runner := issue(runtoken.Claims{Kind: runtoken.KindService, Subject: "service:runner", Audience: []string{"core"},
		Capabilities: []string{runtoken.CapRunnerConnect}})
	run := issue(runtoken.Claims{Kind: runtoken.KindRun, Subject: "run:1", Audience: []string{"core"},
		Customer: "c", Project: "p", Ticket: "t", Capabilities: []string{runtoken.CapCredentialsRead}})

	call := func(path, tok string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, call("/internal/v1/whoami", gateway))
	assert.Equal(t, http.StatusNoContent, call("/internal/v1/secret", gateway))
	assert.Equal(t, http.StatusForbidden, call("/internal/v1/secret", runner), "capability required")
	assert.Equal(t, http.StatusForbidden, call("/internal/v1/secret", run), "run tokens are not service tokens")
	assert.Equal(t, http.StatusUnauthorized, call("/internal/v1/whoami", ""))
	assert.Equal(t, http.StatusUnauthorized, call("/internal/v1/whoami", "not-a-token"))
}
