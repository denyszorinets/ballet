package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// newControl returns a flow environment whose flows, dispatcher and
// scheduler respect the returned Control's pauses.
func newControl(t *testing.T) (flowEnv, *app.Control, *app.Scheduler) {
	t.Helper()
	var ct *app.Control
	e := newFlows(t, always(report.OutcomeDone), func(_ *app.Orchestrator, d *app.Dispatcher, fl *app.Flows) {
		ct = &app.Control{Store: fl.RunStore.(app.ControlStore), Tenancy: fl.Tenancy, Authz: fl.Authz, RunStore: fl.RunStore,
			Dispatcher: d, Flows: fl, Now: time.Now}
		fl.Paused, d.Held = ct.Paused, ct.Paused
	})
	e.forge.checks = forge.ChecksSuccess
	s := &app.Scheduler{Store: e.st, Start: e.flows.StartTicket, Paused: ct.Paused, MaxActive: 10, MaxActivePerProject: 10,
		Interval: time.Hour}
	return e, ct, s
}

func TestControl_PausingAProjectStopsNewStagesUntilResumed(t *testing.T) {
	e, ct, s := newControl(t)
	dave, bob, alice := user(t, "dave", "acme-admins"), user(t, "bob", "acme-devs"), user(t, "alice", "ballet-admins")

	_, err := ct.Pause(bob, "WEB", "")
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers do not manage runs")
	_, err = ct.Pause(dave, "", "")
	assert.ErrorIs(t, err, app.ErrForbidden, "only organization admins pause everything")
	v, err := ct.Pause(dave, "WEB", "Release freeze.")
	require.NoError(t, err)
	assert.Equal(t, "WEB", v.ProjectKey)
	pauses, err := ct.List(bob)
	require.NoError(t, err)
	require.Len(t, pauses, 1)
	assert.Equal(t, "Release freeze.", pauses[0].Reason)

	started := e.ticket(t, auto)
	_, err = e.flows.Start(dave, started.Key)
	require.NoError(t, err)
	e.waitFlow(t, started.Key, func(f app.FlowView) bool { return f.Waiting == "pause" })
	waiting := e.ticket(t, auto)
	n, err := s.Tick(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n, "the scheduler starts nothing in a paused project")
	assert.Empty(t, e.agent.ran())

	require.NoError(t, ct.Resume(dave, "WEB"))
	e.waitFlow(t, started.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	n, err = s.Tick(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, tracker.StateInProgress, e.state(t, waiting.Key))
	assert.ErrorIs(t, ct.Resume(dave, "WEB"), app.ErrNotFound, "not paused any more")

	// The organization pause covers every project.
	_, err = ct.Pause(alice, "", "Incident.")
	require.NoError(t, err)
	assert.True(t, ct.Paused(t.Context(), "anything"))
	require.NoError(t, ct.Resume(alice, ""))
}

func TestControl_KillSwitchCancelsRunsAndTheStageRunsAgainOnResume(t *testing.T) {
	e, ct, _ := newControl(t)
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

	_, n, err := ct.Kill(dave, "WEB", "Runaway agent.")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	killed := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "pause" })
	assert.Equal(t, "implement", killed.Stage)
	r, err := e.st.Run(t.Context(), f.RunID)
	require.NoError(t, err)
	assert.Equal(t, run.StatusCancelled, r.Status)

	// A run queued by hand while paused waits too.
	manual, err := e.flows.Runs.Create(dave, tk.Key, "debug", cmd)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	r, err = e.st.Run(t.Context(), manual.ID)
	require.NoError(t, err)
	assert.Equal(t, run.StatusQueued, r.Status)

	require.NoError(t, ct.Resume(dave, "WEB"))
	resumed := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.RunID != "" && f.RunID != killed.RunID })
	assert.Equal(t, "implement", resumed.Stage)
	assert.NotEqual(t, f.RunID, resumed.RunID, "a new session of the stage")
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), manual.ID)
		return r.Status == run.StatusStarting
	}, 5*time.Second, 10*time.Millisecond, "held runs start after the resume")
}
