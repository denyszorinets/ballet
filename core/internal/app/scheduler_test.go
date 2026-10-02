package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// newScheduler returns a flow environment whose runs never finish, so
// started flows keep their slots.
func newScheduler(t *testing.T, maxActive, perProject int) (flowEnv, *app.Scheduler) {
	t.Helper()
	e := newFlows(t, always(report.OutcomeDone))
	e.runner.mu.Lock()
	e.runner.hold = true
	e.runner.mu.Unlock()
	s := &app.Scheduler{Store: e.st, Start: e.flows.StartTicket, MaxActive: maxActive, MaxActivePerProject: perProject,
		Interval: time.Hour}
	return e, s
}

func (e flowEnv) item(t *testing.T, in app.CreateItemInput, state tracker.State) app.ItemView {
	t.Helper()
	dave := user(t, "dave", "acme-admins")
	in.ProjectKey = "WEB"
	if in.Kind == tracker.KindTicket {
		in.AcceptanceCriteria = []string{"works"}
	}
	it, err := e.tr.CreateItem(dave, in)
	require.NoError(t, err)
	if state != it.State {
		it, err = e.tr.TransitionItem(dave, it.Key, state, it.Version)
		require.NoError(t, err)
	}
	return it
}

func (e flowEnv) started(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		assert.Equal(t, tracker.StateInProgress, e.state(t, k), k)
	}
}

func TestScheduler_StartsOldestReadyTicketsWithinProjectLimit(t *testing.T) {
	e, s := newScheduler(t, 10, 2)
	a := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "A"}, tracker.StateReady)
	b := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "B"}, tracker.StateReady)
	c := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "C"}, tracker.StateReady)
	backlog := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "Later"}, tracker.StateBacklog)

	n, err := s.Tick(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	e.started(t, a.Key, b.Key)
	assert.Equal(t, tracker.StateReady, e.state(t, c.Key), "the project is at its limit")
	assert.Equal(t, tracker.StateBacklog, e.state(t, backlog.Key), "only ready tickets start")

	n, err = s.Tick(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestScheduler_GlobalLimit(t *testing.T) {
	e, s := newScheduler(t, 1, 5)
	a := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "A"}, tracker.StateReady)
	b := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "B"}, tracker.StateReady)
	n, err := s.Tick(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	e.started(t, a.Key)
	assert.Equal(t, tracker.StateReady, e.state(t, b.Key))
}

func TestScheduler_WaitsForBlockers(t *testing.T) {
	e, s := newScheduler(t, 10, 10)
	dave := user(t, "dave", "acme-admins")
	blocker := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "Schema"}, tracker.StateBacklog)
	blocked := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "API"}, tracker.StateReady)
	_, err := e.tr.AddDependency(dave, blocker.Key, app.DirBlocks, blocked.Key)
	require.NoError(t, err)
	epic := e.item(t, app.CreateItemInput{Kind: tracker.KindEpic, Title: "Phase 2"}, tracker.StateOpen)
	inEpic := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "UI", EpicKey: epic.Key}, tracker.StateReady)
	_, err = e.tr.AddDependency(dave, blocker.Key, app.DirBlocks, epic.Key)
	require.NoError(t, err)

	n, err := s.Tick(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n, "blocked tickets and tickets of blocked epics wait")

	// Resolving the blocker unblocks both, without human action.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	_, err = e.tr.TransitionItem(dave, blocker.Key, tracker.StateCancelled, blocker.Version)
	require.NoError(t, err)
	s.Kick()
	assert.Eventually(t, func() bool {
		return e.state(t, blocked.Key) == tracker.StateInProgress && e.state(t, inEpic.Key) == tracker.StateInProgress
	}, 5*time.Second, 10*time.Millisecond)
}

func TestScheduler_SkipsProjectsWithoutRepository(t *testing.T) {
	e, s := newScheduler(t, 10, 10)
	dave := user(t, "dave", "acme-admins")
	it, err := e.tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "APP", Kind: tracker.KindTicket, Title: "X",
		AcceptanceCriteria: []string{"works"}})
	require.NoError(t, err)
	_, err = e.tr.TransitionItem(dave, it.Key, tracker.StateReady, it.Version)
	require.NoError(t, err)
	n, err := s.Tick(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestScheduler_StoppedFlowsFreeSlots(t *testing.T) {
	e, s := newScheduler(t, 1, 1)
	a := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "A"}, tracker.StateReady)
	b := e.item(t, app.CreateItemInput{Kind: tracker.KindTicket, Title: "B"}, tracker.StateReady)
	_, err := s.Tick(t.Context())
	require.NoError(t, err)
	e.started(t, a.Key)

	// A human pauses A: once its run ends, its flow stops and frees the slot.
	dave := user(t, "dave", "acme-admins")
	f := e.waitFlow(t, a.Key, func(f app.FlowView) bool { return f.RunID != "" })
	it, err := e.tr.GetItem(dave, a.Key)
	require.NoError(t, err)
	_, err = e.tr.TransitionItem(dave, a.Key, tracker.StatePaused, it.Version)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), f.RunID)
		return r.Status == run.StatusStarting || r.Status == run.StatusRunning
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, e.runner.d.Finished(t.Context(), "r1", f.RunID, 0, "", false))
	e.waitFlow(t, a.Key, func(f app.FlowView) bool { return f.Status == app.FlowStopped })
	n, err := s.Tick(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	e.started(t, b.Key)
}
