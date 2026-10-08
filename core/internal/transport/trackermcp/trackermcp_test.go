package trackermcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/trackermcp"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
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

type acceptAll struct{}

func (acceptAll) Start(context.Context, run.Run, map[string]string) error { return nil }
func (acceptAll) Cancel(context.Context, string) error                    { return nil }
func (acceptAll) Input(context.Context, string, string, string) error     { return nil }

type fixture struct {
	url    string
	issuer *runtoken.TokenIssuer
	runID  string
	ticket string
	st     *store.Store
}

func setup(t *testing.T) fixture {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	boot, _ := rbac.ParseBootstrap("groups:admins")
	authz := &app.RBAC{Store: st, Bootstrap: []rbac.Binding{boot}}
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"groups": []any{"admins"}}})
	ten := &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	_, err = ten.CreateCustomer(admin, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	_, err = ten.CreateProject(admin, app.CreateProjectInput{CustomerKey: "acme", Key: "WEB", Name: "Web"})
	require.NoError(t, err)
	tr := &app.Tracker{Items: st, Deps: st, Tenancy: st, Events: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	it, err := tr.CreateItem(admin, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Login"})
	require.NoError(t, err)

	d := &app.Dispatcher{Store: st, Tenancy: st, Now: time.Now, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	runs := &app.Runs{Store: st, Items: st, Tenancy: st, Authz: authz, Dispatcher: d, Now: time.Now, NewID: store.NewID}
	r, err := runs.Create(admin, it.Key, "implement", run.Spec{Command: []string{"x"}})
	require.NoError(t, err)
	require.NoError(t, d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, acceptAll{}))
	require.Eventually(t, func() bool { x, _ := st.Run(t.Context(), r.ID); return x.Status == run.StatusStarting },
		5*time.Second, 5*time.Millisecond)

	at := &app.AgentTracker{Reports: st, RunStore: st, Runs: runs, Items: st, Tenancy: st, Authz: authz,
		Changesets: &app.Changesets{Store: st, Tracker: tr}, Now: time.Now, NewID: store.NewID}
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	mux := http.NewServeMux()
	trackermcp.Register(mux, runtoken.NewRingVerifier(ring, time.Now), at, "test")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return fixture{url: srv.URL + trackermcp.Path, issuer: runtoken.NewIssuer(ring, time.Now), runID: r.ID, ticket: it.Key, st: st}
}

func (f fixture) token(t *testing.T, kind runtoken.Kind, ticket string, caps ...string) string {
	t.Helper()
	c := runtoken.Claims{Kind: kind, Subject: "run:" + f.runID, Audience: []string{"core"}, Customer: "acme",
		Project: "WEB", Ticket: ticket, Capabilities: caps}
	if kind == runtoken.KindService {
		c.Subject, c.Project, c.Ticket = "service:x", "", ""
	}
	raw, err := f.issuer.Issue(c, time.Hour)
	require.NoError(t, err)
	return raw
}

func connect(t *testing.T, f fixture, token string) (*mcp.ClientSession, error) {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
	s, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: f.url, HTTPClient: &http.Client{Transport: bearer{token, &http.Transport{}}},
	}, nil)
	if err == nil {
		t.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

func call(t *testing.T, s *mcp.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestTrackerMCP_ToolsWorkOnTheRunsTicket(t *testing.T) {
	f := setup(t)
	s, err := connect(t, f, f.token(t, runtoken.KindRun, f.ticket, runtoken.CapTrackerRead, runtoken.CapTrackerReport))
	require.NoError(t, err)

	tools, err := s.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	assert.ElementsMatch(t, []string{"ticket_context", "report_progress", "submit_stage_report", "record_assumption",
		"raise_question", "propose_work"}, names)

	out, isErr := call(t, s, "report_progress", map[string]any{"message": "Working on the form."})
	require.False(t, isErr, out)
	_, isErr = call(t, s, "submit_stage_report", map[string]any{"outcome": "done", "summary": "Login works."})
	require.False(t, isErr)
	out, isErr = call(t, s, "submit_stage_report", map[string]any{"outcome": "great", "summary": "x"})
	assert.True(t, isErr, "bad outcome")
	assert.Contains(t, out, "outcome")
	_, isErr = call(t, s, "record_assumption", map[string]any{"assumption": "8h sessions"})
	require.False(t, isErr)
	out, isErr = call(t, s, "raise_question", map[string]any{"question": "Which IdP?", "blocking": true})
	require.False(t, isErr)
	assert.Contains(t, out, "end the session now")
	out, isErr = call(t, s, "propose_work", map[string]any{"title": "Rate-limit logins", "description": "d", "reason": "found"})
	require.False(t, isErr, out)
	assert.Contains(t, out, "changeset")

	out, isErr = call(t, s, "ticket_context", map[string]any{})
	require.False(t, isErr, out)
	assert.Contains(t, out, "# "+f.ticket+": Login")
	assert.Contains(t, out, "(stage_report, done): Login works.")
	assert.Contains(t, out, "Which IdP? — open")
}

func TestTrackerMCP_Authorization(t *testing.T) {
	f := setup(t)
	readOnly, err := connect(t, f, f.token(t, runtoken.KindRun, f.ticket, runtoken.CapTrackerRead))
	require.NoError(t, err)
	out, isErr := call(t, readOnly, "report_progress", map[string]any{"message": "x"})
	assert.True(t, isErr)
	assert.Contains(t, out, "tracker.report")
	_, isErr = call(t, readOnly, "ticket_context", map[string]any{})
	assert.False(t, isErr)

	wrong, err := connect(t, f, f.token(t, runtoken.KindRun, "WEB-99", runtoken.CapTrackerRead))
	require.NoError(t, err)
	out, isErr = call(t, wrong, "ticket_context", map[string]any{})
	assert.True(t, isErr, "a token for another ticket")
	assert.Contains(t, out, "forbidden")

	_, err = connect(t, f, f.token(t, runtoken.KindService, "", runtoken.CapTrackerRead))
	assert.Error(t, err, "service tokens are refused")
	_, err = connect(t, f, "forged")
	assert.Error(t, err)
}
