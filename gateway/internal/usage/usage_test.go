package usage_test

import (
	"context"
	"io"
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
	"github.com/denyszorinets/ballet/gateway/internal/fakeprovider"
	"github.com/denyszorinets/ballet/gateway/internal/proxy"
	"github.com/denyszorinets/ballet/gateway/internal/usage"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

type staticCreds struct{ url string }

func (s staticCreds) ResolveCredential(_ context.Context, _, _, _ string) (core.Credential, error) {
	return core.Credential{APIKey: "sk", BaseURL: s.url}, nil
}

func TestMeter_RecordsUsageOfJSONAndStreamingResponses(t *testing.T) {
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "k.json"), time.Now)
	require.NoError(t, err)
	prov := fakeprovider.New(t, "sk")
	var mu sync.Mutex
	var records []usage.Record
	gw := &proxy.Anthropic{
		Verifier:  runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now),
		Core:      staticCreds{prov.URL},
		Transport: &http.Transport{},
		Observe: usage.Observe(func(r usage.Record) {
			mu.Lock()
			records = append(records, r)
			mu.Unlock()
		}, time.Now),
	}
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	tok, err := runtoken.NewIssuer(ring, time.Now).Issue(runtoken.Claims{
		Kind: runtoken.KindRun, Subject: "run:7", Audience: []string{"gateway"},
		Customer: "acme", Project: "WEB", Ticket: "WEB-3", Capabilities: []string{runtoken.CapLLMInvoke},
	}, time.Hour)
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(client.CloseIdleConnections)
	for _, body := range []string{`{"model":"claude-a"}`, `{"model":"claude-b","stream":true}`} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", strings.NewReader(body))
		req.Header.Set("x-api-key", tok)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(records) == 2 }, 2*time.Second, 10*time.Millisecond)
	for i, model := range []string{"claude-a", "claude-b"} {
		r := records[i]
		assert.Equal(t, model, r.Model)
		assert.Equal(t, "run:7", r.Run)
		assert.Equal(t, "WEB-3", r.Ticket)
		assert.Equal(t, int64(fakeprovider.InputTokens), r.InputTokens)
		assert.Equal(t, int64(fakeprovider.OutputTokens), r.OutputTokens, "streams use the final message_delta count")
		assert.Equal(t, int64(fakeprovider.CacheReadTokens), r.CacheRead)
		assert.Equal(t, http.StatusOK, r.Status)
	}
}
