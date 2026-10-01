package embeddings_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/gateway/internal/core"
	"github.com/denyszorinets/ballet/gateway/internal/embeddings"
	"github.com/denyszorinets/ballet/gateway/internal/usage"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/embed"
)

type creds map[string]core.Credential

func (c creds) ResolveCredential(_ context.Context, customer, _, provider string) (core.Credential, error) {
	if cr, ok := c[customer+"/"+provider]; ok {
		return cr, nil
	}
	return core.Credential{}, core.ErrNoCredential
}

func TestEmbeddings(t *testing.T) {
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "k.json"), time.Now)
	require.NoError(t, err)
	issue := func(c runtoken.Claims) string {
		raw, err := runtoken.NewIssuer(ring, time.Now).Issue(c, time.Hour)
		require.NoError(t, err)
		return raw
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer sk-openai", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"object":"list","model":"text-embedding-3-small","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":4,"total_tokens":4}}`))
	}))
	t.Cleanup(provider.Close)
	var mu sync.Mutex
	var records []usage.Record
	h := &embeddings.Handler{
		Verifier: runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now), DefaultModel: embed.HashModel,
		Core: creds{"acme/openai": {APIKey: "sk-openai", BaseURL: provider.URL}},
		Sink: func(r usage.Record) { mu.Lock(); records = append(records, r); mu.Unlock() }, Now: time.Now,
		HTTP: &http.Client{Transport: &http.Transport{}},
	}
	service := issue(runtoken.Claims{Kind: runtoken.KindService, Subject: "service:knowledge", Audience: []string{"gateway"},
		Capabilities: []string{runtoken.CapLLMEmbed}})
	run := issue(runtoken.Claims{Kind: runtoken.KindRun, Subject: "run:1", Audience: []string{"gateway"}, Customer: "acme",
		Project: "WEB", Ticket: "WEB-1", Capabilities: []string{runtoken.CapLLMInvoke}})

	call := func(tok, customer, body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", stringsReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if customer != "" {
			req.Header.Set(embed.HeaderCustomer, customer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := call(service, "acme", `{"input":["export invoices","login"]}`)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, embed.HashModel, out["model"], "default model")
	assert.Len(t, out["data"], 2)

	code, out = call(run, "", `{"model":"text-embedding-3-small","input":"hello world"}`)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "text-embedding-3-small", out["model"])

	code, _ = call(service, "", `{"input":"x"}`)
	assert.Equal(t, http.StatusBadRequest, code, "service tokens must name the customer")
	code, _ = call(service, "globex", `{"model":"text-embedding-3-small","input":"x"}`)
	assert.Equal(t, http.StatusForbidden, code, "no openai credential")
	code, _ = call("bogus", "acme", `{"input":"x"}`)
	assert.Equal(t, http.StatusUnauthorized, code)
	code, _ = call(service, "acme", `{"input":[]}`)
	assert.Equal(t, http.StatusBadRequest, code)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, records, 2)
	assert.Equal(t, "acme", records[0].Customer)
	assert.Equal(t, int64(3), records[0].InputTokens, "hash model counts words")
	assert.Equal(t, "WEB-1", records[1].Ticket)
	assert.Equal(t, int64(4), records[1].InputTokens, "provider-reported tokens")
}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }
