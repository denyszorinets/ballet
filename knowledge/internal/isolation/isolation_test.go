package isolation_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/store"
	"github.com/denyszorinets/ballet/knowledge/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/knowledge/internal/transport/mcpapi"
)

// world has two organizations, acme and globex, with deliberately similar
// knowledge so that any leak would show up in search.
type world struct {
	url        string
	ring       *runtoken.KeyRing
	foreign    *runtoken.KeyRing // keys Knowledge does not trust
	acmeID     string            // an acme entry
	globexID   string            // a globex entry
	acmeTitle  string
	globexBody string
}

func issue(t *testing.T, ring *runtoken.KeyRing, c runtoken.Claims, ttl time.Duration) string {
	t.Helper()
	if c.Subject == "" {
		c.Subject = "service:core"
	}
	if c.Kind == "" {
		c.Kind = runtoken.KindService
	}
	if c.Audience == nil {
		c.Audience = []string{"knowledge"}
	}
	raw, err := runtoken.NewIssuer(ring, time.Now).Issue(c, ttl)
	require.NoError(t, err)
	return raw
}

func setup(t *testing.T) world {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "k.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	emb := app.LocalEmbedders{Embedder: embed.Hash{}}
	ix := &app.Indexer{Store: st, Embedders: emb, Model: embed.HashModel}
	svc := &app.Service{Store: st, Searcher: st, Embedders: emb, Indexer: ix, Now: time.Now,
		NewID: func() string { return uuid.Must(uuid.NewV7()).String() }}
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	foreign, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "foreign.json"), time.Now)
	require.NoError(t, err)
	verifier := runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now)
	mux := http.NewServeMux()
	httpapi.Register(mux, verifier, svc)
	mcpapi.Register(mux, verifier, svc, "test")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	w := world{url: srv.URL, ring: ring, foreign: foreign, acmeTitle: "Acme pricing strategy", globexBody: "Globex merger plans in Q3"}
	write := []string{runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite}
	acme := runtoken.ContextWithClaims(t.Context(), runtoken.Claims{Organization: "acme", Subject: "s", Capabilities: write})
	globex := runtoken.ContextWithClaims(t.Context(), runtoken.Claims{Organization: "globex", Subject: "s", Capabilities: write})
	a, err := svc.Create(acme, "acme", app.CreateInput{Kind: "decision", Title: w.acmeTitle, Body: "Confidential pricing strategy for invoices"})
	require.NoError(t, err)
	g, err := svc.Create(globex, "globex", app.CreateInput{Kind: "decision", Title: "Pricing strategy", Body: w.globexBody + " pricing strategy invoices"})
	require.NoError(t, err)
	w.acmeID, w.globexID = a.ID, g.ID
	_, err = ix.IndexOnce(t.Context())
	require.NoError(t, err)
	return w
}

func rest(t *testing.T, w world, token, method, path, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, w.url+path, rd)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestIsolation_REST(t *testing.T) {
	w := setup(t)
	rw := []string{runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite}
	globexTok := issue(t, w.ring, runtoken.Claims{Organization: "globex", Capabilities: rw}, time.Hour)
	acmePath := "/v1/organizations/acme/knowledge"
	globexPath := "/v1/organizations/globex/knowledge"

	tests := []struct {
		name, token, method, path, body string
		want                            int
	}{
		// A valid globex token used on acme's space.
		{"list foreign space", globexTok, "GET", acmePath + "/entries", "", 403},
		{"get foreign entry in foreign space", globexTok, "GET", acmePath + "/entries/" + w.acmeID, "", 403},
		{"search foreign space", globexTok, "GET", acmePath + "/search?q=pricing", "", 403},
		{"versions in foreign space", globexTok, "GET", acmePath + "/entries/" + w.acmeID + "/versions", "", 403},
		{"create in foreign space", globexTok, "POST", acmePath + "/entries", `{"kind":"note","title":"x"}`, 403},
		{"update in foreign space", globexTok, "PATCH", acmePath + "/entries/" + w.acmeID, `{"version":1,"title":"x"}`, 403},
		// Acme's entry ID used inside globex's own space.
		{"get foreign entry via own space", globexTok, "GET", globexPath + "/entries/" + w.acmeID, "", 404},
		{"update foreign entry via own space", globexTok, "PATCH", globexPath + "/entries/" + w.acmeID, `{"version":1,"title":"pwned"}`, 404},
		{"versions of foreign entry via own space", globexTok, "GET", globexPath + "/entries/" + w.acmeID + "/versions", "", 404},
		// Forged or misdirected tokens.
		{"no token", "", "GET", acmePath + "/entries", "", 401},
		{"token for another audience", issue(t, w.ring, runtoken.Claims{Organization: "acme", Audience: []string{"gateway"}, Capabilities: rw}, time.Hour), "GET", acmePath + "/entries", "", 401},
		{"token signed by an untrusted key", issue(t, w.foreign, runtoken.Claims{Organization: "acme", Capabilities: rw}, time.Hour), "GET", acmePath + "/entries", "", 401},
		{"token without organization", issue(t, w.ring, runtoken.Claims{Capabilities: rw}, time.Hour), "GET", acmePath + "/entries", "", 403},
		{"token without capability", issue(t, w.ring, runtoken.Claims{Organization: "acme"}, time.Hour), "GET", acmePath + "/entries", "", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := rest(t, w, tt.token, tt.method, tt.path, tt.body)
			assert.Equal(t, tt.want, code, body)
			assert.NotContains(t, body, w.acmeTitle)
			assert.NotContains(t, body, "Confidential")
		})
	}

	// The acme entry is untouched.
	acmeTok := issue(t, w.ring, runtoken.Claims{Organization: "acme", Capabilities: rw}, time.Hour)
	code, body := rest(t, w, acmeTok, "GET", acmePath+"/entries/"+w.acmeID, "")
	require.Equal(t, 200, code)
	assert.Contains(t, body, w.acmeTitle)
	assert.Contains(t, body, `"version":1`)
}

func TestIsolation_ExpiredToken(t *testing.T) {
	w := setup(t)
	past := time.Now().Add(-time.Hour)
	ring := w.ring
	raw, err := runtoken.NewIssuer(ring, func() time.Time { return past }).Issue(runtoken.Claims{
		Kind: runtoken.KindService, Subject: "service:core", Audience: []string{"knowledge"},
		Organization: "acme", Capabilities: []string{runtoken.CapKnowledgeRead},
	}, time.Minute)
	require.NoError(t, err)

	code, _ := rest(t, w, raw, "GET", "/v1/organizations/acme/knowledge/entries", "")

	assert.Equal(t, 401, code)
}

func TestIsolation_SearchNeverLeaks(t *testing.T) {
	w := setup(t)
	for _, c := range []string{"acme", "globex"} {
		tok := issue(t, w.ring, runtoken.Claims{Organization: c, Capabilities: []string{runtoken.CapKnowledgeRead}}, time.Hour)
		code, body := rest(t, w, tok, "GET", "/v1/organizations/"+c+"/knowledge/search?q=pricing+strategy+invoices+merger", "")
		require.Equal(t, 200, code)
		var out struct {
			Items []struct {
				Entry struct{ ID string } `json:"entry"`
			} `json:"items"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &out))
		require.Len(t, out.Items, 1, "%s sees exactly its own entry", c)
		want := map[string]string{"acme": w.acmeID, "globex": w.globexID}[c]
		assert.Equal(t, want, out.Items[0].Entry.ID)
	}
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func TestIsolation_MCP(t *testing.T) {
	w := setup(t)
	tok := issue(t, w.ring, runtoken.Claims{Kind: runtoken.KindRun, Subject: "run:9", Organization: "globex", Project: "GLX",
		Ticket: "GLX-1", Capabilities: []string{runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite}}, time.Hour)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
	s, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: w.url + mcpapi.Path, HTTPClient: &http.Client{Transport: bearer{tok, &http.Transport{}}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	calls := []struct {
		name string
		args map[string]any
	}{
		{"knowledge_get", map[string]any{"id": w.acmeID}},
		{"knowledge_update", map[string]any{"id": w.acmeID, "version": 1, "title": "pwned"}},
	}
	for _, c := range calls {
		res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: c.name, Arguments: c.args})
		require.NoError(t, err)
		assert.True(t, res.IsError, "%s on another organization's entry must fail", c.name)
		b, _ := json.Marshal(res)
		assert.NotContains(t, string(b), w.acmeTitle)
	}
	for name, args := range map[string]map[string]any{
		"knowledge_search": {"query": "pricing strategy"},
		"knowledge_list":   {},
	} {
		res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		require.False(t, res.IsError, name)
		b, _ := json.Marshal(res.StructuredContent)
		assert.NotContains(t, string(b), w.acmeID, "%s must not return acme entries", name)
		assert.Contains(t, string(b), w.globexID)
	}
}
