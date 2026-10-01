package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

// fakeRunner records what Core asks of it.
type fakeRunner struct {
	mu        sync.Mutex
	started   []string
	cancelled []string
	refuse    bool
}

func (f *fakeRunner) Start(_ context.Context, r run.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse {
		return errors.New("busy")
	}
	f.started = append(f.started, r.ID)
	return nil
}

func (f *fakeRunner) Cancel(_ context.Context, runID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, runID)
	return nil
}

func (f *fakeRunner) starts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

type runsEnv struct {
	runs *app.Runs
	d    *app.Dispatcher
	tr   *app.Tracker
	st   *store.Store
}

func newRuns(t *testing.T, grace time.Duration) runsEnv {
	t.Helper()
	tr, env := newTracker(t)
	d := &app.Dispatcher{Store: env.store, Tenancy: env.store, Now: time.Now, Grace: grace, MaxLogBytes: 20,
		Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return runsEnv{
		runs: &app.Runs{Store: env.store, Items: env.store, Tenancy: env.store, Authz: env.rbac, Dispatcher: d,
			Now: time.Now, NewID: store.NewID},
		d: d, tr: tr, st: env.store,
	}
}

var cmd = run.Spec{Command: []string{"make", "test"}}

func (e runsEnv) status(t *testing.T, id string) run.Run {
	t.Helper()
	r, err := e.st.Run(t.Context(), id)
	require.NoError(t, err)
	return r
}

func (e runsEnv) eventually(t *testing.T, id string, want run.Status) run.Run {
	t.Helper()
	require.Eventually(t, func() bool { return e.status(t, id).Status == want }, 5*time.Second, 5*time.Millisecond,
		"run %s never became %s", id, want)
	return e.status(t, id)
}

func TestRuns_CreateIsForRunManagersOnTickets(t *testing.T) {
	e := newRuns(t, time.Minute)
	dave := user(t, "dave", "acme-admins")
	bob := user(t, "bob", "acme-devs")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	ep := mk(t, e.tr, tracker.KindEpic, "e")

	_, err := e.runs.Create(bob, tk.Key, "implement", cmd)
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers do not queue runs by hand")
	_, err = e.runs.Create(dave, ep.Key, "implement", cmd)
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = e.runs.Create(dave, tk.Key, "Implement!", cmd)
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = e.runs.Create(dave, tk.Key, "implement", run.Spec{})
	assert.ErrorIs(t, err, app.ErrInvalid)

	r, err := e.runs.Create(dave, tk.Key, "implement", cmd)
	require.NoError(t, err)
	assert.Equal(t, run.StatusQueued, r.Status)
	list, err := e.runs.ListForTicket(bob, tk.Key)
	require.NoError(t, err)
	assert.Len(t, list, 1, "engineers read runs")
}

func TestRuns_DispatchRespectsCapacityAndRecordsReports(t *testing.T) {
	e := newRuns(t, time.Minute)
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	a, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	b, _ := e.runs.Create(dave, tk.Key, "review", cmd)

	fr := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, fr))
	assert.ErrorIs(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, &fakeRunner{}), app.ErrRunnerConflict)
	e.eventually(t, a.ID, run.StatusStarting)
	assert.Equal(t, []string{a.ID}, fr.starts())
	assert.Equal(t, run.StatusQueued, e.status(t, b.ID).Status, "capacity 1")

	require.NoError(t, e.d.Running(t.Context(), "r1", a.ID))
	require.NoError(t, e.d.Log(t.Context(), "r1", a.ID, "stdout", "ok 1\n"))
	require.NoError(t, e.d.Log(t.Context(), "r1", a.ID, "stdout", "ok 2\n"))
	require.NoError(t, e.d.Log(t.Context(), "r1", a.ID, "stdout", "this chunk exceeds the log limit\n"))
	logs, err := e.runs.Logs(dave, a.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, logs, 2, "output beyond MaxLogBytes is dropped")
	assert.Equal(t, "ok 2\n", logs[1].Text)
	later, _ := e.runs.Logs(dave, a.ID, logs[0].Seq, 0)
	assert.Len(t, later, 1)

	assert.ErrorIs(t, e.d.Log(t.Context(), "r2", a.ID, "stdout", "x"), app.ErrForbidden, "only the run's runner reports")
	require.NoError(t, e.d.Finished(t.Context(), "r1", a.ID, 0, "", false))
	done := e.status(t, a.ID)
	assert.Equal(t, run.StatusSucceeded, done.Status)
	assert.Equal(t, 0, *done.ExitCode)
	assert.False(t, done.StartedAt.IsZero())
	assert.ErrorIs(t, e.d.Finished(t.Context(), "r1", a.ID, 0, "", false), app.ErrForbidden, "finished runs take no reports")

	e.eventually(t, b.ID, run.StatusStarting)
	require.NoError(t, e.d.Finished(t.Context(), "r1", b.ID, 2, "", false))
	failed := e.status(t, b.ID)
	assert.Equal(t, run.StatusFailed, failed.Status)
	assert.Equal(t, "exited with code 2", failed.Error)
}

func TestRuns_CancelQueuedAndActive(t *testing.T) {
	e := newRuns(t, time.Minute)
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	q, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	got, err := e.runs.Cancel(dave, q.ID)
	require.NoError(t, err)
	assert.Equal(t, run.StatusCancelled, got.Status)
	_, err = e.runs.Cancel(dave, q.ID)
	assert.ErrorIs(t, err, app.ErrConflict)

	fr := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 2}, fr))
	a, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	e.eventually(t, a.ID, run.StatusStarting)
	_, err = e.runs.Cancel(dave, a.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{a.ID}, fr.cancelled, "the runner is asked to stop")
	require.NoError(t, e.d.Finished(t.Context(), "r1", a.ID, -1, "", true))
	assert.Equal(t, run.StatusCancelled, e.status(t, a.ID).Status)
}

func TestRuns_RefusedRunsAreRequeued(t *testing.T) {
	e := newRuns(t, time.Minute)
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	r, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	fr := &fakeRunner{refuse: true}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, fr))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, run.StatusQueued, e.status(t, r.ID).Status)
	assert.Empty(t, e.status(t, r.ID).Runner)
}

func TestRuns_ReconnectAndDisconnect(t *testing.T) {
	e := newRuns(t, 100*time.Millisecond)
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	a, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	b, _ := e.runs.Create(dave, tk.Key, "review", cmd)
	first := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 2}, first))
	e.eventually(t, a.ID, run.StatusStarting)
	e.eventually(t, b.ID, run.StatusStarting)

	// The runner reconnects still executing a, but lost b.
	e.d.Disconnect("r1", first)
	second := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 2, Active: []string{a.ID}}, second))
	assert.Equal(t, run.StatusStarting, e.status(t, a.ID).Status)
	assert.Equal(t, run.StatusFailed, e.status(t, b.ID).Status)
	assert.Len(t, e.d.Runners(), 1)

	// Gone for longer than Grace: its runs fail.
	e.d.Disconnect("r1", second)
	failed := e.eventually(t, a.ID, run.StatusFailed)
	assert.Equal(t, "the runner disconnected", failed.Error)
}
