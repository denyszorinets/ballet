package agentapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/agentapi"
	"github.com/denyszorinets/ballet/kit/agentproto"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/rpc"
)

type env struct {
	url    string
	issuer *runtoken.TokenIssuer
	runs   *app.Runs
	st     *store.Store
	ticket string
}

func setup(t *testing.T) env {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	boot, _ := rbac.ParseBootstrap("groups:admins")
	authz := &app.RBAC{Store: st, Bootstrap: []rbac.Binding{boot}}
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"groups": []any{"admins"}}})
	ten := &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	_, err = ten.CreateOrganization(admin, app.CreateOrganizationInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	_, err = ten.CreateProject(admin, app.CreateProjectInput{OrganizationKey: "acme", Key: "WEB", Name: "Web"})
	require.NoError(t, err)
	tr := &app.Tracker{Items: st, Deps: st, Tenancy: st, Events: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	it, err := tr.CreateItem(admin, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "t"})
	require.NoError(t, err)

	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	d := &app.Dispatcher{Store: st, Tenancy: st, Now: time.Now, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	mux := http.NewServeMux()
	agentapi.Register(mux, agentapi.Deps{Verifier: runtoken.NewRingVerifier(ring, time.Now), Dispatcher: d})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return env{
		url: "ws" + strings.TrimPrefix(srv.URL, "http") + agentproto.Path, issuer: runtoken.NewIssuer(ring, time.Now),
		runs: &app.Runs{Store: st, Items: st, Tenancy: st, Authz: authz, Dispatcher: d, Now: time.Now, NewID: store.NewID},
		st:   st, ticket: it.Key,
	}
}

func (e env) token(t *testing.T, caps ...string) string {
	t.Helper()
	tok, err := e.issuer.Issue(runtoken.Claims{Kind: runtoken.KindService, Subject: "service:agent",
		Audience: []string{"core"}, Capabilities: caps}, time.Hour)
	require.NoError(t, err)
	return tok
}

// fake is a test agent: it records run.start requests.
type fake struct {
	mu     sync.Mutex
	starts []agentproto.Start
}

func (r *fake) handler(_ context.Context, req *rpc.Request) (any, error) {
	if req.Method == agentproto.MethodStart {
		var s agentproto.Start
		if err := req.Decode(&s); err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.starts = append(r.starts, s)
		r.mu.Unlock()
	}
	return struct{}{}, nil
}

func (e env) dial(t *testing.T, tok string, r *fake) (*rpc.Conn, error) {
	t.Helper()
	c, _, err := rpc.Dial(t.Context(), e.url, rpc.DialOptions{
		Token:   func(context.Context) (string, error) { return tok, nil },
		Options: rpc.Options{Handler: r.handler},
	})
	if err == nil {
		t.Cleanup(func() { _ = c.Close() })
	}
	return c, err
}

func TestAgentAPI_RunLifecycle(t *testing.T) {
	e := setup(t)
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"groups": []any{"admins"}}})
	r := &fake{}
	c, err := e.dial(t, e.token(t, runtoken.CapAgentConnect), r)
	require.NoError(t, err)

	err = c.Call(t.Context(), agentproto.MethodLog, agentproto.Log{Run: "x", Text: "x"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeInvalidRequest), "hello first: %v", err)
	require.NoError(t, c.Call(t.Context(), agentproto.MethodHello, agentproto.Hello{Agent: "r1", Capacity: 1}, nil))

	v, err := e.runs.Create(admin, e.ticket, "implement", run.Spec{Command: []string{"echo", "hi"}, Env: map[string]string{"A": "1"}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.starts) == 1 }, 5*time.Second, 5*time.Millisecond)
	r.mu.Lock()
	assert.Equal(t, agentproto.Start{Run: v.ID, Spec: agentproto.Spec{Command: []string{"echo", "hi"}, Env: map[string]string{"A": "1"}}}, r.starts[0])
	r.mu.Unlock()

	require.NoError(t, c.Call(t.Context(), agentproto.MethodStatus, agentproto.Status{Run: v.ID, Status: "running"}, nil))
	require.NoError(t, c.Call(t.Context(), agentproto.MethodLog, agentproto.Log{Run: v.ID, Stream: "stdout", Text: "hi\n"}, nil))
	require.NoError(t, c.Call(t.Context(), agentproto.MethodFinished, agentproto.Finished{Run: v.ID, ExitCode: 0}, nil))
	got, err := e.runs.Get(admin, v.ID)
	require.NoError(t, err)
	assert.Equal(t, run.StatusSucceeded, got.Status)
	assert.Equal(t, "r1", got.Agent)
	logs, err := e.runs.Logs(admin, v.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "hi\n", logs[0].Text)

	err = c.Call(t.Context(), agentproto.MethodFinished, agentproto.Finished{Run: v.ID}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeForbidden), "%v", err)

	second, err := e.dial(t, e.token(t, runtoken.CapAgentConnect), &fake{})
	require.NoError(t, err)
	err = second.Call(t.Context(), agentproto.MethodHello, agentproto.Hello{Agent: "r1", Capacity: 1}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeConflict), "one connection per agent name: %v", err)
}

func TestAgentAPI_RequiresAgentToken(t *testing.T) {
	e := setup(t)
	_, err := e.dial(t, e.token(t, runtoken.CapUsageWrite), &fake{})
	assert.Error(t, err)
	_, err = e.dial(t, "forged", &fake{})
	assert.Error(t, err)
}
