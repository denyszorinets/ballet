package proxy_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/gateway/internal/core"
	"github.com/denyszorinets/ballet/gateway/internal/fakeprovider"
	"github.com/denyszorinets/ballet/gateway/internal/proxy"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

type creds map[string]core.Credential // "organization/project" → credential

func (c creds) ResolveCredential(_ context.Context, organization, project, _ string) (core.Credential, error) {
	if cr, ok := c[organization+"/"+project]; ok {
		return cr, nil
	}
	return core.Credential{}, core.ErrNoCredential
}

// budgets refuses project FULL and fails for project BROKEN.
type budgets struct{}

func (budgets) CheckBudget(_ context.Context, _, project, _ string) (bool, string, error) {
	switch project {
	case "FULL":
		return false, "The daily budget of the project is used up.", nil
	case "BROKEN":
		return false, "", errors.New("core unavailable")
	}
	return true, "", nil
}

type fixture struct {
	client   *http.Client
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
		Core:       creds{"acme/WEB": {APIKey: "sk-real", BaseURL: prov.URL}, "acme/BROKEN": {APIKey: "sk-real", BaseURL: prov.URL}},
		Budget:     budgets{},
		DefaultURL: "http://127.0.0.1:1",
		// A transport per test: no pooled connections shared between tests.
		Transport: &http.Transport{DialContext: closeTracer(t)},
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", gw)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	issuer := runtoken.NewIssuer(ring, time.Now)
	client := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(client.CloseIdleConnections)
	return fixture{client: client, url: srv.URL, provider: prov, issue: func(project string, caps ...string) string {
		raw, err := issuer.Issue(runtoken.Claims{
			Kind: runtoken.KindRun, Subject: "run:1", Audience: []string{proxy.Audience},
			Organization: "acme", Project: project, Ticket: "WEB-1", Capabilities: caps,
		}, time.Hour)
		require.NoError(t, err)
		return raw
	}}
}

func post(t *testing.T, f fixture, header map[string]string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.url+"/v1/messages", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestProxy_ForwardsWithTheRealKey(t *testing.T) {
	f := setup(t)
	tok := f.issue("WEB", runtoken.CapLLMInvoke)

	resp := post(t, f, map[string]string{"Authorization": "Bearer " + tok}, `{"model":"claude-x","max_tokens":10}`)

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

	resp := post(t, f, map[string]string{"x-api-key": f.issue("WEB", runtoken.CapLLMInvoke)}, `{"model":"m"}`)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "sk-real", f.provider.Requests()[0].APIKey)
}

func TestProxy_StreamsIncrementally(t *testing.T) {
	f := setup(t)
	start := time.Now()

	resp := post(t, f, map[string]string{"Authorization": "Bearer " + f.issue("WEB", runtoken.CapLLMInvoke)},
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
			resp := post(t, f, tt.header, `{}`)
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

// closeTracer dials TCP connections that record who closes them and, if
// the test fails, logs those stacks: TestProxy_StreamsIncrementally fails
// rarely in CI with the gateway's upstream connection closed locally
// mid-stream (#88), and this shows by whom.
func closeTracer(t *testing.T) func(ctx context.Context, network, addr string) (net.Conn, error) {
	var mu sync.Mutex
	var stacks []string
	t.Cleanup(func() {
		if t.Failed() {
			mu.Lock()
			defer mu.Unlock()
			for _, s := range stacks {
				t.Logf("upstream connection closed by:\n%s", s)
			}
		}
	})
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &tracedConn{Conn: c, record: func(s string) { mu.Lock(); stacks = append(stacks, s); mu.Unlock() }}, nil
	}
}

type tracedConn struct {
	net.Conn
	once   sync.Once
	record func(string)
}

func (c *tracedConn) Close() error {
	c.once.Do(func() { c.record(string(debug.Stack())) })
	return c.Conn.Close()
}

func TestProxy_RejectsOversizedRequests(t *testing.T) {
	f := setup(t)
	body := `{"model":"m","pad":"` + strings.Repeat("x", 33<<20) + `"}`
	resp := post(t, f, map[string]string{"Authorization": "Bearer " + f.issue("WEB", runtoken.CapLLMInvoke)}, body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.Empty(t, f.provider.Requests(), "nothing is forwarded")
}

func TestProxy_RefusesWorkOverBudget(t *testing.T) {
	f := setup(t)
	body := `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
	resp := post(t, f, map[string]string{"x-api-key": f.issue("FULL", runtoken.CapLLMInvoke)}, body)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	data, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(data), "Ballet budget exhausted: The daily budget of the project is used up.")

	ok := post(t, f, map[string]string{"x-api-key": f.issue("BROKEN", runtoken.CapLLMInvoke)}, body)
	defer func() { _ = ok.Body.Close() }()
	assert.Equal(t, http.StatusOK, ok.StatusCode, "a failed budget check lets the call through")
}
