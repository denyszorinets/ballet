package core_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/gateway/internal/core"
)

func TestClient_ResolvesWithServiceTokenAndCaches(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Bearer svc", r.Header.Get("Authorization"))
		if r.URL.Query().Get("project") == "NONE" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		assert.Equal(t, "acme", r.URL.Query().Get("organization"))
		_, _ = w.Write([]byte(`{"provider":"anthropic","api_key":"sk","base_url":""}`))
	}))
	t.Cleanup(srv.Close)
	c := &core.Client{BaseURL: srv.URL, Token: func(context.Context) (string, error) { return "svc", nil }}

	for range 3 {
		cred, err := c.ResolveCredential(t.Context(), "acme", "WEB", "anthropic")
		require.NoError(t, err)
		assert.Equal(t, "sk", cred.APIKey)
	}
	assert.Equal(t, int32(1), calls.Load(), "cached")

	_, err := c.ResolveCredential(t.Context(), "acme", "NONE", "anthropic")
	assert.ErrorIs(t, err, core.ErrNoCredential)
}

func TestClient_ChecksBudgetsAndCachesVerdicts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/internal/v1/budget/check", r.URL.Path)
		if r.URL.Query().Get("ticket") == "WEB-1" {
			_, _ = w.Write([]byte(`{"allowed":false,"reason":"used up"}`))
			return
		}
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	t.Cleanup(srv.Close)
	c := &core.Client{BaseURL: srv.URL, Token: func(context.Context) (string, error) { return "svc", nil }}

	for range 2 {
		ok, reason, err := c.CheckBudget(t.Context(), "acme", "WEB", "WEB-1")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, "used up", reason)
	}
	ok, _, err := c.CheckBudget(t.Context(), "acme", "WEB", "WEB-2")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int32(2), calls.Load(), "verdicts are cached per ticket")
}
