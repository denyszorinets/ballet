package app_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

func newOrchestrator(t *testing.T, st *store.Store) (*app.Orchestrator, func()) {
	t.Helper()
	o := &app.Orchestrator{Store: st, Now: time.Now, Interval: 10 * time.Millisecond, Lease: 30 * time.Second, // never expires in a test run
		Backoff: func(int) time.Duration { return 10 * time.Millisecond }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	start := func() {
		go func() { o.Run(ctx); close(done) }()
		t.Cleanup(func() { cancel(); <-done })
	}
	return o, start
}

func waitJob(t *testing.T, st *store.Store, id string, want app.JobStatus) app.Job {
	t.Helper()
	var j app.Job
	require.Eventually(t, func() bool {
		var err error
		j, err = st.Job(t.Context(), id)
		return err == nil && j.Status == want
	}, 5*time.Second, 5*time.Millisecond, "job %s never became %s", id, want)
	return j
}

func TestOrchestrator_RunsRetriesAndKillsJobs(t *testing.T) {
	env := newRBACEnv(t)
	o, start := newOrchestrator(t, env.store)
	var runs atomic.Int32
	var seen sync.Map
	o.Handle("echo", func(_ context.Context, j app.Job) error {
		seen.Store(j.ID, string(j.Payload))
		return nil
	})
	o.Handle("flaky", func(context.Context, app.Job) error {
		if runs.Add(1) < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	o.Handle("broken", func(context.Context, app.Job) error { return errors.New("always") })
	o.Handle("permanent", func(context.Context, app.Job) error { return fmt.Errorf("%w: bad input", app.ErrPermanent) })
	o.Handle("panics", func(context.Context, app.Job) error { panic("boom") })
	var finished sync.Map
	o.OnFinished = func(kind, result string) { finished.Store(kind+":"+result, true) }
	start()

	now := time.Now()
	require.NoError(t, o.Enqueue(t.Context(),
		app.NewJob("j1", "echo", "", map[string]int{"n": 1}, now, 3),
		app.NewJob("j2", "flaky", "", nil, now, 5),
		app.NewJob("j3", "broken", "", nil, now, 2),
		app.NewJob("j4", "permanent", "", nil, now, 5),
		app.NewJob("j5", "unknown", "", nil, now, 5),
		app.NewJob("j6", "panics", "", nil, now, 1),
	))
	waitJob(t, env.store, "j1", app.JobDone)
	p, _ := seen.Load("j1")
	assert.JSONEq(t, `{"n":1}`, p.(string))
	assert.Equal(t, 3, waitJob(t, env.store, "j2", app.JobDone).Attempts)
	dead := waitJob(t, env.store, "j3", app.JobDead)
	assert.Equal(t, 2, dead.Attempts)
	assert.Equal(t, "always", dead.LastError)
	assert.Equal(t, 1, waitJob(t, env.store, "j4", app.JobDead).Attempts, "permanent failures are not retried")
	assert.Contains(t, waitJob(t, env.store, "j5", app.JobDead).LastError, "no handler")
	assert.Contains(t, waitJob(t, env.store, "j6", app.JobDead).LastError, "panicked")
	_, ok := finished.Load("flaky:retry")
	assert.True(t, ok)
}

func TestOrchestrator_TimersAndDedupe(t *testing.T) {
	env := newRBACEnv(t)
	o, start := newOrchestrator(t, env.store)
	var count atomic.Int32
	o.Handle("tick", func(context.Context, app.Job) error { count.Add(1); return nil })
	start()

	later := time.Now().Add(300 * time.Millisecond)
	require.NoError(t, o.Enqueue(t.Context(), app.NewJob("t1", "tick", "check:WEB-1", nil, later, 3)))
	require.NoError(t, o.Enqueue(t.Context(), app.NewJob("t2", "tick", "check:WEB-1", nil, later, 3)),
		"a live dedupe key skips the job")
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, count.Load(), "not due yet")
	waitJob(t, env.store, "t1", app.JobDone)
	_, err := env.store.Job(t.Context(), "t2")
	assert.ErrorIs(t, err, app.ErrNotFound)

	require.NoError(t, o.Enqueue(t.Context(), app.NewJob("t3", "tick", "check:WEB-1", nil, time.Now(), 3)),
		"the key is free once the job is done")
	waitJob(t, env.store, "t3", app.JobDone)
	assert.Equal(t, int32(2), count.Load())
}

func TestOrchestrator_ExpiredLeasesAreReclaimed(t *testing.T) {
	env := newRBACEnv(t)
	// A job claimed by an orchestrator that died: running, lease expired.
	j := app.NewJob("lost", "work", "", nil, time.Now(), 3)
	j.Status, j.Attempts, j.LeaseUntil = app.JobRunning, 1, time.Now().Add(-time.Second)
	require.NoError(t, env.store.EnqueueJobs(t.Context(), j))

	o, start := newOrchestrator(t, env.store)
	o.Handle("work", func(context.Context, app.Job) error { return nil })
	start()
	assert.Equal(t, 2, waitJob(t, env.store, "lost", app.JobDone).Attempts)
}

func TestOrchestrator_ConcurrentOrchestratorsClaimOnce(t *testing.T) {
	env := newRBACEnv(t)
	var mu sync.Mutex
	ran := map[string]int{}
	handler := func(_ context.Context, j app.Job) error {
		mu.Lock()
		ran[j.ID]++
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return nil
	}
	for range 3 {
		o, start := newOrchestrator(t, env.store)
		o.Lease = time.Minute
		o.Handle("work", handler)
		start()
	}
	var jobs []app.Job
	for i := range 40 {
		jobs = append(jobs, app.NewJob(fmt.Sprintf("c%d", i), "work", "", nil, time.Now(), 3))
	}
	require.NoError(t, env.store.EnqueueJobs(t.Context(), jobs...))
	for i := range 40 {
		waitJob(t, env.store, fmt.Sprintf("c%d", i), app.JobDone)
	}
	mu.Lock()
	defer mu.Unlock()
	for id, n := range ran {
		assert.Equal(t, 1, n, id)
	}
	assert.Len(t, ran, 40)
}
