package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

func TestAgentTracker_RunsReportOnTheirTicketOnly(t *testing.T) {
	e := newRuns(t, time.Minute)
	cs := &app.Changesets{Store: e.st, Tracker: e.tr}
	at := &app.AgentTracker{Reports: e.st, RunStore: e.st, Runs: e.runs, Items: e.st, Tenancy: e.st, Authz: e.runs.Authz,
		Changesets: cs, Now: time.Now, NewID: store.NewID}
	dave := user(t, "dave", "acme-admins")
	tk, err := e.tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Login",
		AcceptanceCriteria: []string{"OIDC"}})
	require.NoError(t, err)
	other := mk(t, e.tr, tracker.KindTicket, "Other")
	r, err := e.runs.Create(dave, tk.Key, "implement", cmd)
	require.NoError(t, err)
	me := app.RunCaller{RunID: r.ID, Customer: "acme", Project: "WEB", Ticket: tk.Key}

	_, err = at.Context(t.Context(), me)
	assert.ErrorIs(t, err, app.ErrForbidden, "a queued run is not active yet")
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, &fakeRunner{}))
	e.eventually(t, r.ID, run.StatusStarting)

	_, err = at.Report(t.Context(), me, report.KindProgress, "", "Started on the login form.", "")
	require.NoError(t, err)
	_, err = at.Report(t.Context(), me, report.KindAssumption, "", "Sessions last 8 hours.", "Common default.")
	require.NoError(t, err)
	_, err = at.Report(t.Context(), me, report.KindStageReport, "maybe", "x", "")
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = at.Report(t.Context(), me, report.KindStageReport, report.OutcomeDone, "Login works; tests added.", "Details.")
	require.NoError(t, err)
	q, err := at.RaiseQuestion(t.Context(), me, "Which IdP for staff?", "Keycloak or Entra.", true)
	require.NoError(t, err)
	assert.Equal(t, report.QuestionOpen, q.Status)

	text, err := at.Context(t.Context(), me)
	require.NoError(t, err)
	assert.Contains(t, text, "# "+tk.Key+": Login")
	assert.Contains(t, text, "- [ ] OIDC")
	assert.Contains(t, text, "(stage_report, done): Login works; tests added.")
	assert.Contains(t, text, "Which IdP for staff? — open")

	for name, c := range map[string]app.RunCaller{
		"other ticket":   {RunID: r.ID, Customer: "acme", Project: "WEB", Ticket: other.Key},
		"other customer": {RunID: r.ID, Customer: "globex", Project: "WEB", Ticket: tk.Key},
		"unknown run":    {RunID: "nope", Customer: "acme", Project: "WEB", Ticket: tk.Key},
	} {
		_, err := at.Report(t.Context(), c, report.KindProgress, "", "x", "")
		assert.ErrorIs(t, err, app.ErrForbidden, name)
	}

	v, err := at.ProposeWork(t.Context(), me, "Rate-limit logins", "Brute force protection.", "", "Found while testing.")
	require.NoError(t, err)
	assert.Equal(t, changeset.StatusProposed, v.Status)
	assert.Equal(t, "run:"+r.ID, v.ProposedBy.Subject)
	assert.Contains(t, v.Summary, "Found while testing.")
	applied, err := cs.Apply(dave, v.ID, []int{0, 1})
	require.NoError(t, err)
	deps, _ := e.tr.Dependencies(dave, tk.Key)
	require.Len(t, deps, 1)
	assert.Equal(t, applied.Results[0].Key, deps[0].Other.Key, "the new ticket relates to the run's ticket")

	reports, questions, err := at.TicketReports(user(t, "bob", "acme-devs"), tk.Key)
	require.NoError(t, err)
	assert.Len(t, reports, 3)
	assert.Len(t, questions, 1)
	_, _, err = at.TicketReports(user(t, "eve"), tk.Key)
	assert.ErrorIs(t, err, app.ErrForbidden)

	require.NoError(t, e.d.Finished(t.Context(), "r1", r.ID, 0, "", false))
	_, err = at.Report(t.Context(), me, report.KindProgress, "", "late", "")
	assert.ErrorIs(t, err, app.ErrForbidden, "finished runs no longer report")
}
