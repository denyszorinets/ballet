package app_test

import (
	"strings"
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

func TestAssumptions_ReviewAndFollowUps(t *testing.T) {
	e := newRuns(t, time.Minute)
	cs := &app.Changesets{Store: e.st, Tracker: e.tr}
	at := &app.AgentTracker{Reports: e.st, RunStore: e.st, Runs: e.runs, Items: e.st, Tenancy: e.st, Authz: e.runs.Authz,
		Changesets: cs, Now: time.Now, NewID: store.NewID}
	as := &app.Assumptions{Store: e.st, Questions: e.st, Reports: e.st, Items: e.st, Tenancy: e.st, Authz: e.runs.Authz,
		Changesets: cs, Now: time.Now, NewID: store.NewID}
	dave, bob, carol := user(t, "dave", "acme-admins"), user(t, "bob", "acme-devs"), user(t, "carol", "acme-viewers")
	tk, err := e.tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Login",
		AcceptanceCriteria: []string{"OIDC"}})
	require.NoError(t, err)
	r, err := e.runs.Create(dave, tk.Key, "implement", cmd)
	require.NoError(t, err)
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, &fakeRunner{}))
	e.eventually(t, r.ID, run.StatusStarting)
	me := app.RunCaller{RunID: r.ID, Customer: "acme", Project: "WEB", Ticket: tk.Key}
	assume := func(text string) report.Report {
		rep, err := at.Report(t.Context(), me, report.KindAssumption, "", text, "Common default.")
		require.NoError(t, err)
		return rep
	}
	sessions, cookies, locale := assume("Sessions last 8 hours."), assume("Cookies are SameSite=Lax."), assume("UI is English only.")
	progress, err := at.Report(t.Context(), me, report.KindProgress, "", "Working.", "")
	require.NoError(t, err)

	open, err := as.List(carol, "WEB", "open")
	require.NoError(t, err)
	assert.Len(t, open, 3)
	assert.Equal(t, tk.Key, open[0].TicketKey)
	_, err = as.List(bob, "WEB", "maybe")
	assert.ErrorIs(t, err, app.ErrInvalid)

	_, err = as.Review(carol, sessions.ID, true, "")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = as.Review(bob, progress.ID, true, "")
	assert.ErrorIs(t, err, app.ErrNotFound, "only assumptions are reviewed")
	_, err = as.Review(bob, sessions.ID, false, " ")
	assert.ErrorIs(t, err, app.ErrInvalid, "a rejection needs a comment")

	ok, err := as.Review(bob, sessions.ID, true, "")
	require.NoError(t, err)
	assert.Equal(t, report.ReviewConfirmed, ok.Review)
	_, err = as.Review(bob, sessions.ID, false, "no")
	assert.ErrorIs(t, err, app.ErrConflict)

	// The ticket is unresolved: the rejection reaches its next sessions.
	rejected, err := as.Review(bob, cookies.ID, false, "Use SameSite=Strict.")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(rejected.FollowUp, "question:"), rejected.FollowUp)
	qs, err := e.st.Questions(t.Context(), tk.ID)
	require.NoError(t, err)
	require.Len(t, qs, 1)
	assert.Equal(t, report.QuestionAnswered, qs[0].Status)
	assert.Equal(t, "Use SameSite=Strict.", qs[0].Answer)
	assert.False(t, qs[0].Blocking)
	text, err := at.Context(t.Context(), me)
	require.NoError(t, err)
	assert.Contains(t, text, "REJECTED by a human: Use SameSite=Strict.")
	assert.Contains(t, text, "Sessions last 8 hours. — confirmed by a human")

	// Once the ticket is resolved, a rejection proposes a correction ticket.
	it, err := e.tr.GetItem(dave, tk.Key)
	require.NoError(t, err)
	_, err = e.tr.TransitionItem(dave, tk.Key, tracker.StateDone, it.Version)
	require.NoError(t, err)
	rejected, err = as.Review(bob, locale.ID, false, "German too.")
	require.NoError(t, err)
	id, isChangeset := strings.CutPrefix(rejected.FollowUp, "changeset:")
	require.True(t, isChangeset, rejected.FollowUp)
	proposal, err := cs.Get(bob, id)
	require.NoError(t, err)
	assert.Equal(t, changeset.StatusProposed, proposal.Status)
	assert.Contains(t, proposal.Ops[0].Create.Description, "German too.")

	all, err := as.List(bob, "WEB", "rejected")
	require.NoError(t, err)
	assert.Len(t, all, 2)
}
