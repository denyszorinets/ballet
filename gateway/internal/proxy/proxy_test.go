package proxy_test

import (
	"bufio"
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

	"github.com/denyszorinets/ballet/gateway/internal/core"
	"github.com/denyszorinets/ballet/gateway/internal/fakeprovider"
	"github.com/denyszorinets/ballet/gateway/internal/proxy"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

type creds map[string]core.Credential // "customer/project" → credential

func (c creds) ResolveCredential(_ context.Context, customer, project, _ string) (core.Credential, error) {
	if cr, ok := c[customer+"/"+project]; ok {
		return cr, nil
	}
	return core.Credential{}, core.ErrNoCredential
}

type fixture struct {
	url      string
	provider *fakeprovider.Provider
	issue    func(project string, caps ...string) string
}

func setup(t *testing.T) fixture {
	t.Helper()
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	prov := fakeprovider.New(t, "sk-real")
	gw := &proxy.Anthropic{
		Verifier:   runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now),
		Core:       creds{"acme/WEB": {APIKey: "sk-real", BaseURL: prov.URL}},
		DefaultURL: "http://127.0.0.1:1",
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", gw)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	issuer := runtoken.NewIssuer(ring, time.Now)
	return fixture{url: srv.URL, provider: prov, issue: func(project string, caps ...string) string {
		raw, err := issuer.Issue(runtoken.Claims{
			Kind: runtoken.KindRun, Subject: "run:1", Audience: []string{proxy.Audience},
			Customer: "acme", Project: project, Ticket: "WEB-1", Capabilities: caps,
		}, time.Hour)
		require.NoError(t, err)
		return raw
	}}
}

func post(t *testing.T, url string, header map[string]string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/v1/messages", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestProxy_ForwardsWithTheRealKey(t *testing.T) {
	f := setup(t)
	tok := f.issue("WEB", runtoken.CapLLMInvoke)

	resp := post(t, f.url, map[string]string{"Authorization": "Bearer " + tok}, `{"model":"claude-x","max_tokens":10}`)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "message", body["type"])
	reqs := f.provider.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, "sk-real", reqs[0].APIKey)
	assert.Empty(t, reqs[0].Headers.Get("Authorization"), "the run token is not forwarded")
	assert.Equal(t, "2023-06-01", reqs[0].Headers.Get("anthropic-version"))
	assert.Equal(t, "/v1/messages", reqs[0].Path)
}

func TestProxy_AcceptsTheRunTokenAsAPIKey(t *testing.T) {
	f := setup(t)

	resp := post(t, f.url, map[string]string{"x-api-key": f.issue("WEB", runtoken.CapLLMInvoke)}, `{"model":"m"}`)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "sk-real", f.provider.Requests()[0].APIKey)
}

func TestProxy_StreamsIncrementally(t *testing.T) {
	f := setup(t)
	start := time.Now()

	resp := post(t, f.url, map[string]string{"Authorization": "Bearer " + f.issue("WEB", runtoken.CapLLMInvoke)},
		`{"model":"m","stream":true}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	r := bufio.NewReader(resp.Body)
	first, err := r.ReadString('\n')
	require.NoError(t, err)
	firstAt := time.Since(start)
	rest, err := io.ReadAll(r)
	require.NoError(t, err)
	total := time.Since(start)

	assert.Equal(t, "event: message_start\n", first)
	assert.Less(t, firstAt, 150*time.Millisecond, "first event must not wait for the whole stream")
	assert.GreaterOrEqual(t, total, 200*time.Millisecond)
	assert.Contains(t, string(rest), "message_stop")
}

func TestProxy_Rejections(t *testing.T) {
	f := setup(t)
	tests := []struct {
		name   string
		header map[string]string
		status int
		typ    string
	}{
		{"no token", nil, 401, "authentication_error"},
		{"invalid token", map[string]string{"Authorization": "Bearer nope"}, 401, "authentication_error"},
		{"missing capability", map[string]string{"Authorization": "Bearer " + f.issue("WEB", runtoken.CapKnowledgeRead)}, 403, "permission_error"},
		{"no credential", map[string]string{"Authorization": "Bearer " + f.issue("APP", runtoken.CapLLMInvoke)}, 403, "permission_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := post(t, f.url, tt.header, `{}`)
			assert.Equal(t, tt.status, resp.StatusCode)
			var body struct {
				Error struct{ Type string } `json:"error"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			assert.Equal(t, tt.typ, body.Error.Type)
		})
	}
	assert.Empty(t, f.provider.Requests(), "rejected requests never reach the provider")
}
