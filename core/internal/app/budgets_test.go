package app_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

// newBudgeted returns a flow environment whose budget verdict is the
// value of exceeded (nil: within budget).
func newBudgeted(t *testing.T, script func(string, int) report.Outcome) (flowEnv, *atomic.Pointer[app.Exceeded]) {
	t.Helper()
	var exceeded atomic.Pointer[app.Exceeded]
	e := newFlows(t, script, func(_ *app.Orchestrator, _ *app.Dispatcher, fl *app.Flows) {
		fl.Budget = func(context.Context, tracker.Item) (*app.Exceeded, error) { return exceeded.Load(), nil }
	})
	e.forge.checks = forge.ChecksSuccess
	return e, &exceeded
}

func TestBudgets_UsedUpTicketBudgetAsksTheHumans(t *testing.T) {
	e, exceeded := newBudgeted(t, always(report.OutcomeDone))
	exceeded.Store(&app.Exceeded{Scope: "ticket", Used: 1200, Limit: 1000})
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)

	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "question" })
	assert.Empty(t, e.runner.ran(), "no session starts over budget")
	assert.Equal(t, tracker.StateWaitingForAnswer, e.state(t, tk.Key))
	qs, err := e.st.Questions(t.Context(), tk.ID)
	require.NoError(t, err)
	require.Len(t, qs, 1)
	assert.Contains(t, qs[0].Context, "budget of the ticket is used up: 1200 of 1000 tokens")

	// A human raises the budget and answers: the stage starts.
	exceeded.Store(nil)
	questions := &app.Questions{Store: e.st, Items: e.st, Tenancy: e.st, Authz: e.flows.Authz,
		Orchestrator: e.flows.Orchestrator, Now: time.Now, NewID: store.NewID}
	_, err = questions.Answer(user(t, "bob", "acme-devs"), qs[0].ID, "Raised to 5000.")
	require.NoError(t, err)
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
}

func TestBudgets_UsedUpDailyBudgetWaitsUntilThereIsBudget(t *testing.T) {
	e, exceeded := newBudgeted(t, always(report.OutcomeDone))
	exceeded.Store(&app.Exceeded{Scope: "project", Used: 50, Limit: 50})
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "budget" })

	other := e.ticket(t, auto)
	s := &app.Scheduler{Store: e.st, Start: e.flows.StartTicket, Budget: e.flows.Budget, MaxActive: 5,
		MaxActivePerProject: 5, Interval: time.Hour}
	n, err := s.Tick(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n, "the scheduler starts nothing over budget")

	rc := &app.Reconciler{Flows: e.flows, Jobs: e.st}
	require.NoError(t, rc.Reconcile(t.Context()))
	assert.Equal(t, "budget", e.waitFlow(t, tk.Key, func(app.FlowView) bool { return true }).Waiting, "still no budget")

	exceeded.Store(nil) // a new day
	require.NoError(t, rc.Reconcile(t.Context()))
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	assert.Equal(t, tracker.StateReady, e.state(t, other.Key))
}

func TestBudgets_AStageCutOffByTheGatewayWaitsInsteadOfFailing(t *testing.T) {
	e, exceeded := newBudgeted(t, always(report.OutcomeDone))
	e.hold()
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	f := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.RunID != "" })
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), f.RunID)
		return r.Status == run.StatusStarting
	}, 5*time.Second, 10*time.Millisecond)

	// The gateway starts refusing calls; the session fails.
	exceeded.Store(&app.Exceeded{Scope: "customer", Used: 10, Limit: 10})
	require.NoError(t, e.runner.d.Finished(t.Context(), "r1", f.RunID, 1, "", false))
	w := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "budget" })
	assert.Equal(t, "implement", w.Stage, "the stage runs again once there is budget")
}
