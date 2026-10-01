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
		assert.Equal(t, "acme", r.URL.Query().Get("customer"))
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
