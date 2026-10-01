package mcpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	"github.com/denyszorinets/ballet/knowledge/internal/transport/mcpapi"
)

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

type fixture struct {
	url   string
	issue func(customer, project string, caps ...string) string
	ix    *app.Indexer
}

func setup(t *testing.T) fixture {
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
	mux := http.NewServeMux()
	mcpapi.Register(mux, runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now), svc, "test")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	issuer := runtoken.NewIssuer(ring, time.Now)
	return fixture{url: srv.URL + mcpapi.Path, ix: ix, issue: func(customer, project string, caps ...string) string {
		raw, err := issuer.Issue(runtoken.Claims{Kind: runtoken.KindRun, Subject: "run:42", Audience: []string{"knowledge"},
			Customer: customer, Project: project, Ticket: project + "-1", Capabilities: caps}, time.Hour)
		require.NoError(t, err)
		return raw
	}}
}

func connect(t *testing.T, f fixture, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: f.url, HTTPClient: &http.Client{Transport: bearer{token, &http.Transport{}}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(t *testing.T, s *mcp.ClientSession, name string, args any) (map[string]any, bool) {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	if res.IsError {
		return nil, true
	}
	b, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	return out, false
}

func TestMCP_AgentWorksWithKnowledge(t *testing.T) {
	f := setup(t)
	s := connect(t, f, f.issue("acme", "WEB", runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite))

	tools, err := s.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	assert.ElementsMatch(t, []string{"knowledge_search", "knowledge_list", "knowledge_get", "knowledge_create", "knowledge_update"}, names)

	created, isErr := callTool(t, s, "knowledge_create", map[string]any{
		"kind": "decision", "title": "Invoices exported as CSV", "body": "CSV keeps the accounting import simple.", "items": []string{"WEB-7"},
	})
	require.False(t, isErr)
	assert.Equal(t, []any{"WEB"}, created["projects"], "defaults to the run's project")
	id := created["id"].(string)
	_, _ = f.ix.IndexOnce(t.Context())

	found, isErr := callTool(t, s, "knowledge_search", map[string]any{"query": "csv invoices"})
	require.False(t, isErr)
	require.Len(t, found["entries"], 1)
	assert.Equal(t, id, found["entries"].([]any)[0].(map[string]any)["id"])

	got, _ := callTool(t, s, "knowledge_get", map[string]any{"id": id})
	assert.Equal(t, "CSV keeps the accounting import simple.", got["body"])
	assert.Equal(t, "run:42", got["updated_by"], "agents are recorded as authors")

	updated, isErr := callTool(t, s, "knowledge_update", map[string]any{"id": id, "version": 1, "body": "CSV, decided in WEB-7."})
	require.False(t, isErr)
	assert.EqualValues(t, 2, updated["version"])
	_, isErr = callTool(t, s, "knowledge_update", map[string]any{"id": id, "version": 1, "body": "stale"})
	assert.True(t, isErr, "stale version rejected")

	listed, _ := callTool(t, s, "knowledge_list", map[string]any{"item": "WEB-7"})
	assert.Len(t, listed["entries"], 1)
}

func TestMCP_ScopeAndCapabilities(t *testing.T) {
	f := setup(t)
	acme := connect(t, f, f.issue("acme", "WEB", runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite))
	created, _ := callTool(t, acme, "knowledge_create", map[string]any{"kind": "note", "title": "Acme secret"})
	id := created["id"].(string)

	globex := connect(t, f, f.issue("globex", "GLX", runtoken.CapKnowledgeRead))
	_, isErr := callTool(t, globex, "knowledge_get", map[string]any{"id": id})
	assert.True(t, isErr, "another customer cannot read the entry")
	listed, _ := callTool(t, globex, "knowledge_list", map[string]any{})
	assert.Empty(t, listed["entries"])

	readOnly := connect(t, f, f.issue("acme", "WEB", runtoken.CapKnowledgeRead))
	_, isErr = callTool(t, readOnly, "knowledge_create", map[string]any{"kind": "note", "title": "x"})
	assert.True(t, isErr, "write needs knowledge.write")

	client := mcp.NewClient(&mcp.Implementation{Name: "anon", Version: "1"}, nil)
	_, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: f.url, HTTPClient: &http.Client{Transport: &http.Transport{}},
	}, nil)
	assert.Error(t, err, "no token, no session")
}
