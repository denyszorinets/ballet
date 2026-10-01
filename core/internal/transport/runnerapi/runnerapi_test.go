package runnerapi_test

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
	"github.com/denyszorinets/ballet/core/internal/transport/runnerapi"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/rpc"
	"github.com/denyszorinets/ballet/kit/runnerproto"
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
	_, err = ten.CreateCustomer(admin, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	_, err = ten.CreateProject(admin, app.CreateProjectInput{CustomerKey: "acme", Key: "WEB", Name: "Web"})
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
	runnerapi.Register(mux, runnerapi.Deps{Verifier: runtoken.NewRingVerifier(ring, time.Now), Dispatcher: d})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return env{
		url: "ws" + strings.TrimPrefix(srv.URL, "http") + runnerproto.Path, issuer: runtoken.NewIssuer(ring, time.Now),
		runs: &app.Runs{Store: st, Items: st, Tenancy: st, Authz: authz, Dispatcher: d, Now: time.Now, NewID: store.NewID},
		st:   st, ticket: it.Key,
	}
}

func (e env) token(t *testing.T, caps ...string) string {
	t.Helper()
	tok, err := e.issuer.Issue(runtoken.Claims{Kind: runtoken.KindService, Subject: "service:runner",
		Audience: []string{"core"}, Capabilities: caps}, time.Hour)
	require.NoError(t, err)
	return tok
}

// runner is a test Runner: it records run.start requests.
type runner struct {
	mu     sync.Mutex
	starts []runnerproto.Start
}

func (r *runner) handler(_ context.Context, req *rpc.Request) (any, error) {
	if req.Method == runnerproto.MethodStart {
		var s runnerproto.Start
		if err := req.Decode(&s); err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.starts = append(r.starts, s)
		r.mu.Unlock()
	}
	return struct{}{}, nil
}

func (e env) dial(t *testing.T, tok string, r *runner) (*rpc.Conn, error) {
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

func TestRunnerAPI_RunLifecycle(t *testing.T) {
	e := setup(t)
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"groups": []any{"admins"}}})
	r := &runner{}
	c, err := e.dial(t, e.token(t, runtoken.CapRunnerConnect), r)
	require.NoError(t, err)

	err = c.Call(t.Context(), runnerproto.MethodLog, runnerproto.Log{Run: "x", Text: "x"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeInvalidRequest), "hello first: %v", err)
	require.NoError(t, c.Call(t.Context(), runnerproto.MethodHello, runnerproto.Hello{Runner: "r1", Capacity: 1}, nil))

	v, err := e.runs.Create(admin, e.ticket, "implement", run.Spec{Command: []string{"echo", "hi"}, Env: map[string]string{"A": "1"}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.starts) == 1 }, 5*time.Second, 5*time.Millisecond)
	r.mu.Lock()
	assert.Equal(t, runnerproto.Start{Run: v.ID, Spec: runnerproto.Spec{Command: []string{"echo", "hi"}, Env: map[string]string{"A": "1"}}}, r.starts[0])
	r.mu.Unlock()

	require.NoError(t, c.Call(t.Context(), runnerproto.MethodStatus, runnerproto.Status{Run: v.ID, Status: "running"}, nil))
	require.NoError(t, c.Notify(t.Context(), runnerproto.MethodLog, runnerproto.Log{Run: v.ID, Stream: "stdout", Text: "hi\n"}))
	require.NoError(t, c.Call(t.Context(), runnerproto.MethodFinished, runnerproto.Finished{Run: v.ID, ExitCode: 0}, nil))
	got, err := e.runs.Get(admin, v.ID)
	require.NoError(t, err)
	assert.Equal(t, run.StatusSucceeded, got.Status)
	assert.Equal(t, "r1", got.Runner)
	logs, err := e.runs.Logs(admin, v.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "hi\n", logs[0].Text)

	err = c.Call(t.Context(), runnerproto.MethodFinished, runnerproto.Finished{Run: v.ID}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeForbidden), "%v", err)

	second, err := e.dial(t, e.token(t, runtoken.CapRunnerConnect), &runner{})
	require.NoError(t, err)
	err = second.Call(t.Context(), runnerproto.MethodHello, runnerproto.Hello{Runner: "r1", Capacity: 1}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeConflict), "one connection per runner name: %v", err)
}

func TestRunnerAPI_RequiresRunnerToken(t *testing.T) {
	e := setup(t)
	_, err := e.dial(t, e.token(t, runtoken.CapUsageWrite), &runner{})
	assert.Error(t, err)
	_, err = e.dial(t, "forged", &runner{})
	assert.Error(t, err)
}
