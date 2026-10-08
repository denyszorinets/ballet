package link_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/agent/internal/link"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/rpc"
	"github.com/denyszorinets/ballet/kit/runnerproto"
)

// core is a fake Core: it records what the agent reports.
type core struct {
	mu       sync.Mutex
	conns    []*rpc.Conn
	hellos   []runnerproto.Hello
	statuses []string
	logs     map[string]string
	finished map[string]runnerproto.Finished
	release  chan struct{} // ends "gate" runs
}

func (c *core) handler(_ context.Context, req *rpc.Request) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch req.Method {
	case runnerproto.MethodHello:
		var h runnerproto.Hello
		_ = req.Decode(&h)
		c.hellos = append(c.hellos, h)
		c.conns = append(c.conns, req.Conn)
	case runnerproto.MethodStatus:
		var s runnerproto.Status
		_ = req.Decode(&s)
		c.statuses = append(c.statuses, s.Run)
	case runnerproto.MethodLog:
		var l runnerproto.Log
		_ = req.Decode(&l)
		c.logs[l.Run] += l.Stream + ":" + l.Text
	case runnerproto.MethodFinished:
		var f runnerproto.Finished
		_ = req.Decode(&f)
		c.finished[f.Run] = f
	}
	return struct{}{}, nil
}

func (c *core) conn() *rpc.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conns[len(c.conns)-1]
}

func (c *core) result(t *testing.T, run string) runnerproto.Finished {
	t.Helper()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		_, ok := c.finished[run]
		return ok
	}, 5*time.Second, 5*time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finished[run]
}

// backend echoes the command; "sleep" blocks until cancelled; "gate"
// until release is closed; "fail" errors.
type backend struct{ release chan struct{} }

func (b backend) Run(ctx context.Context, _ string, spec runnerproto.Spec, out func(string, string)) (int, *runnerproto.Result, error) {
	switch spec.Command[0] {
	case "sleep":
		out("stdout", "sleeping\n")
		<-ctx.Done()
		return -1, nil, ctx.Err()
	case "gate":
		<-b.release
		return 0, nil, nil
	case "fail":
		return -1, nil, errors.New("image not found")
	case "agent":
		return 0, &runnerproto.Result{Success: true, Summary: "done", Turns: 2}, nil
	}
	out("stdout", "a ")
	out("stdout", "b\n")
	out("stderr", "warn\n")
	return 3, nil, nil
}

func setup(t *testing.T, capacity int) (*core, *link.Agent) {
	t.Helper()
	c := &core{logs: map[string]string{}, finished: map[string]runnerproto.Finished{}, release: make(chan struct{})}
	srv := httptest.NewServer(rpc.NewServer(rpc.ServerOptions{
		Options: rpc.Options{Handler: c.handler},
		Authenticate: func(_ context.Context, tok string) (auth.Identity, time.Time, error) {
			if tok != "agent-token" {
				return auth.Identity{}, time.Time{}, errors.New("bad token")
			}
			return auth.Identity{Kind: auth.KindService, Subject: "service:agent"}, time.Time{}, nil
		},
	}))
	t.Cleanup(srv.Close)
	r := &link.Agent{
		URL:   "ws" + strings.TrimPrefix(srv.URL, "http"),
		Token: func(context.Context) (string, error) { return "agent-token", nil },
		Name:  "r1", Labels: map[string]string{"backend": "fake"}, Capacity: capacity, Backend: backend{release: c.release},
		FlushInterval: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	require.Eventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.hellos) > 0 }, 5*time.Second, 5*time.Millisecond)
	return c, r
}

func start(t *testing.T, c *core, run string, command string, timeout int) error {
	t.Helper()
	return c.conn().Call(t.Context(), runnerproto.MethodStart, runnerproto.Start{Run: run,
		Spec: runnerproto.Spec{Command: []string{command}, TimeoutSeconds: timeout}}, nil)
}

func TestAgent_ExecutesAndReports(t *testing.T) {
	c, _ := setup(t, 2)
	assert.Equal(t, runnerproto.Hello{Runner: "r1", Labels: map[string]string{"backend": "fake"}, Capacity: 2, Active: []string{}}, c.hellos[0])

	require.NoError(t, start(t, c, "run1", "echo", 0))
	f := c.result(t, "run1")
	assert.Equal(t, runnerproto.Finished{Run: "run1", ExitCode: 3}, f)
	c.mu.Lock()
	assert.Equal(t, "stdout:a b\nstderr:warn\n", c.logs["run1"], "batched per stream, in order")
	assert.Equal(t, []string{"run1"}, c.statuses)
	c.mu.Unlock()

	require.NoError(t, start(t, c, "run2", "fail", 0))
	assert.Equal(t, "image not found", c.result(t, "run2").Error)

	require.NoError(t, start(t, c, "run3", "agent", 0))
	assert.Equal(t, &runnerproto.Result{Success: true, Summary: "done", Turns: 2}, c.result(t, "run3").Result,
		"the session's result is reported")
}

func TestAgent_CancelTimeoutCapacity(t *testing.T) {
	c, _ := setup(t, 1)
	require.NoError(t, start(t, c, "long", "sleep", 0))
	err := start(t, c, "other", "echo", 0)
	assert.True(t, rpc.IsCode(err, rpc.CodeConflict), "at capacity: %v", err)

	require.NoError(t, c.conn().Call(t.Context(), runnerproto.MethodCancel, runnerproto.Cancel{Run: "long"}, nil))
	assert.True(t, c.result(t, "long").Cancelled)
	err = c.conn().Call(t.Context(), runnerproto.MethodCancel, runnerproto.Cancel{Run: "long"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeNotFound))

	require.NoError(t, start(t, c, "slow", "sleep", 1))
	f := c.result(t, "slow")
	assert.Equal(t, "the run timed out", f.Error)
	assert.False(t, f.Cancelled)
}

func TestAgent_ReconnectsAndDeliversResults(t *testing.T) {
	c, _ := setup(t, 1)
	require.NoError(t, start(t, c, "long", "sleep", 0))
	require.Eventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.statuses) == 1 }, 5*time.Second, 5*time.Millisecond)

	// Core drops the connection; the agent comes back with its active run.
	_ = c.conn().Close()
	require.Eventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.hellos) == 2 }, 10*time.Second, 10*time.Millisecond)
	c.mu.Lock()
	assert.Equal(t, []string{"long"}, c.hellos[1].Active)
	c.mu.Unlock()

	require.NoError(t, c.conn().Call(t.Context(), runnerproto.MethodCancel, runnerproto.Cancel{Run: "long"}, nil))
	assert.True(t, c.result(t, "long").Cancelled)
}

func TestAgent_KeepsResultsFinishedWhileDisconnected(t *testing.T) {
	c, _ := setup(t, 1)
	require.NoError(t, start(t, c, "quick", "gate", 0))
	require.Eventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.statuses) == 1 }, 5*time.Second, 5*time.Millisecond)

	// The run ends while Core is away: on reconnect the agent still
	// claims it, so Core waits for its result instead of failing it.
	_ = c.conn().Close()
	close(c.release)
	require.Eventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.hellos) == 2 }, 10*time.Second, 10*time.Millisecond)
	c.mu.Lock()
	assert.Equal(t, []string{"quick"}, c.hellos[1].Active)
	c.mu.Unlock()
	assert.Equal(t, 0, c.result(t, "quick").ExitCode)
}
