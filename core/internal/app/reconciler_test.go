package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

func (e flowEnv) reconciler() *app.Reconciler {
	return &app.Reconciler{Flows: e.flows, Jobs: e.st}
}

func (e flowEnv) hold() {
	e.runner.mu.Lock()
	e.runner.hold = true
	e.runner.mu.Unlock()
}

func (e flowEnv) events(t *testing.T, key string) []string {
	t.Helper()
	h, err := e.tr.ItemHistory(user(t, "bob", "acme-devs"), key)
	require.NoError(t, err)
	var out []string
	for _, ev := range h {
		out = append(out, ev.Type)
	}
	return out
}

func TestReconciler_RequeuesLostFollowUps(t *testing.T) {
	// Core stopped between finishing a run and queuing the flow's next
	// step: the run ended, nobody told the flow.
	e := newFlows(t, always(report.OutcomeDone), func(_ *app.Orchestrator, d *app.Dispatcher) { d.OnFinished = nil })
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	f := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.RunID != "" })
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), f.RunID)
		return r.Status.Terminal()
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "implement", f.Stage)

	require.NoError(t, e.reconciler().Reconcile(t.Context()))
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Stage == "review" })
}

func TestReconciler_FlagsStuckRuns(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	e.hold()
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	f := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.RunID != "" })
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), f.RunID)
		return r.Status == run.StatusStarting
	}, 5*time.Second, 10*time.Millisecond)

	rc := e.reconciler()
	require.NoError(t, rc.Reconcile(t.Context()))
	assert.Equal(t, app.FlowRunning, e.waitFlow(t, tk.Key, func(app.FlowView) bool { return true }).Status, "within its timeout")

	rc.Now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	require.NoError(t, rc.Reconcile(t.Context()))
	flagged := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "question" })
	assert.Contains(t, flagged.Report, "made no progress")
	assert.Equal(t, tracker.StateWaitingForAnswer, e.state(t, tk.Key))
	assert.Contains(t, e.events(t, tk.Key), "flow.stuck")
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), f.RunID)
		return r.Status == run.StatusCancelled
	}, 5*time.Second, 10*time.Millisecond, "the stuck run is cancelled")
	qs, err := e.st.Questions(t.Context(), tk.ID)
	require.NoError(t, err)
	require.Len(t, qs, 1)
	assert.True(t, qs[0].Blocking)
}

func TestReconciler_FlagsStepsThatFailedForGood(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone), func(o *app.Orchestrator, _ *app.Dispatcher) {
		o.Handle(app.JobFlowEnter, func(context.Context, app.Job) error { return app.ErrPermanent })
	})
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		jobs, err := e.st.FlowJobs(t.Context(), tk.ID)
		return err == nil && len(jobs) == 1 && jobs[0].Status == app.JobDead
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, e.reconciler().Reconcile(t.Context()))
	f := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "question" })
	assert.Contains(t, f.Report, "Core could not continue the implement stage")
}

func TestReconciler_StopsFlowsOfPausedTickets(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	dave := user(t, "dave", "acme-admins")
	_, err := e.flows.Pipelines.Save(dave, "WEB", "default", pipeline.Definition{MaxIterations: 1, Stages: []pipeline.Stage{
		{ID: "approve", Kind: pipeline.KindHuman},
	}}, 0)
	require.NoError(t, err)
	waiting := e.ticket(t, auto)
	_, err = e.flows.Start(dave, waiting.Key)
	require.NoError(t, err)
	e.waitFlow(t, waiting.Key, func(f app.FlowView) bool { return f.Waiting == "approval" })

	it, err := e.tr.GetItem(dave, waiting.Key)
	require.NoError(t, err)
	_, err = e.tr.TransitionItem(dave, waiting.Key, tracker.StatePaused, it.Version)
	require.NoError(t, err)
	require.NoError(t, e.reconciler().Reconcile(t.Context()))
	e.waitFlow(t, waiting.Key, func(f app.FlowView) bool { return f.Status == app.FlowStopped })
}

func TestReconciler_PausingCancelsTheRunningStage(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	e.hold()
	dave := user(t, "dave", "acme-admins")
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(dave, tk.Key)
	require.NoError(t, err)
	f := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.RunID != "" })
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), f.RunID)
		return r.Status == run.StatusStarting
	}, 5*time.Second, 10*time.Millisecond)
	it, err := e.tr.GetItem(dave, tk.Key)
	require.NoError(t, err)
	_, err = e.tr.TransitionItem(dave, tk.Key, tracker.StatePaused, it.Version)
	require.NoError(t, err)

	require.NoError(t, e.reconciler().Reconcile(t.Context()))
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowStopped })
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), f.RunID)
		return r.Status == run.StatusCancelled
	}, 5*time.Second, 10*time.Millisecond)
}

func TestReconciler_CancelsOrphanedStageRuns(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	e.hold()
	tk := e.ticket(t, auto)
	it, err := e.st.ItemByKey(t.Context(), tk.Key)
	require.NoError(t, err)
	v, err := e.flows.Runs.CreateForStage(t.Context(), it, "implement", "claude-code", app.StageOptions{}, 0)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), v.Run.ID)
		return r.Status == run.StatusStarting
	}, 5*time.Second, 10*time.Millisecond)

	rc := e.reconciler()
	require.NoError(t, rc.Reconcile(t.Context()))
	r, err := e.st.Run(t.Context(), v.Run.ID)
	require.NoError(t, err)
	assert.False(t, r.Status.Terminal(), "young runs may still be recorded by their flow")

	rc.Now = func() time.Time { return time.Now().Add(time.Hour) }
	require.NoError(t, rc.Reconcile(t.Context()))
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), v.Run.ID)
		return r.Status == run.StatusCancelled
	}, 5*time.Second, 10*time.Millisecond)
}
