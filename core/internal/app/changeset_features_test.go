package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

func newFeatureChangesets(t *testing.T) (*app.Changesets, *app.Tracker, *app.Features) {
	t.Helper()
	tr, env := newTracker(t)
	fs := &app.Features{Store: env.store, Tenancy: env.store, Items: env.store, Authz: env.rbac, Now: time.Now, NewID: store.NewID}
	return &app.Changesets{Store: env.store, Tracker: tr, Features: fs}, tr, fs
}

// exportPlan derives a scheduled export from an existing feature and plans
// a ticket changing both.
func exportPlan(existing string) app.ProposeInput {
	desc := "Exports invoices as CSV, on demand or on a schedule."
	return app.ProposeInput{ProjectKey: "WEB", Title: "Scheduled export", Ops: []changeset.Op{
		{Kind: changeset.OpCreateFeature, Ref: "scheduled", Feature: &changeset.CreateFeature{
			Title: "Scheduled export", Description: "Runs exports nightly.", Projects: []string{"WEB", "APP"}}},
		{Kind: changeset.OpUpdateFeature, FeatureUpdate: &changeset.UpdateFeature{Feature: existing, Description: &desc}},
		{Kind: changeset.OpLinkFeatures, FeatureLink: &changeset.LinkFeatures{From: "$scheduled", To: existing, Type: feature.LinkDerivedFrom}},
		{Kind: changeset.OpCreateItem, Ref: "cron", Create: &changeset.CreateItem{Kind: tracker.KindTicket, Title: "Nightly export job",
			Features: []string{"$scheduled", existing}}},
	}}
}

func TestChangesets_FeatureOperationsApplyWithTheirTickets(t *testing.T) {
	cs, _, fs := newFeatureChangesets(t)
	bob := user(t, "bob", "acme-devs")
	_, err := fs.Create(bob, "acme", app.CreateFeatureInput{Title: "Invoice export", Description: "CSV.", ProjectKeys: []string{"WEB"}})
	require.NoError(t, err)

	proposed, err := cs.Propose(bob, exportPlan("F-1"))
	require.NoError(t, err)
	list, err := fs.List(bob, "acme", app.FeatureFilter{})
	require.NoError(t, err)
	assert.Len(t, list, 1, "proposing changes nothing")

	applied, err := cs.Apply(bob, proposed.ID, []int{0, 1, 2, 3})
	require.NoError(t, err)
	assert.Equal(t, "F-2", applied.Results[0].Key)
	assert.Equal(t, "F-1", applied.Results[1].Key)
	assert.NotEmpty(t, applied.Results[2].FeatureLinkID)
	ticket := applied.Results[3].Key

	created, err := fs.Get(bob, "acme", "F-2")
	require.NoError(t, err)
	assert.Equal(t, []string{"APP", "WEB"}, created.ProjectKeys)
	require.Len(t, created.Links, 1)
	assert.Equal(t, feature.LinkDerivedFrom, created.Links[0].Type)
	require.Len(t, created.Tickets, 1)
	assert.Equal(t, ticket, created.Tickets[0].Key)

	revs, err := fs.Revisions(bob, "acme", "F-1")
	require.NoError(t, err)
	require.Len(t, revs, 2)
	assert.Equal(t, "Changeset “Scheduled export”", revs[0].Reason)
	assert.Equal(t, feature.Cause{Kind: "changeset", Ref: proposed.ID}, revs[0].Cause)
	assert.Equal(t, feature.ReviewNone, revs[0].Review, "approved by a human")

	features, err := fs.TicketFeatures(bob, ticket)
	require.NoError(t, err)
	require.Len(t, features, 2)
	assert.Equal(t, "F-1", features[0].Key)
}

func TestChangesets_FeatureOperationsAreValidatedAgainstTheMap(t *testing.T) {
	cs, tr, fs := newFeatureChangesets(t)
	bob := user(t, "bob", "acme-devs")
	_, err := cs.Propose(bob, exportPlan("F-9"))
	assert.ErrorIs(t, err, app.ErrInvalid, "unknown feature")

	_, err = fs.Create(bob, "acme", app.CreateFeatureInput{Title: "Invoice export"})
	require.NoError(t, err)
	in := exportPlan("F-1")
	in.Ops[0].Feature.Projects = []string{"GLX"}
	_, err = cs.Propose(bob, in)
	assert.ErrorIs(t, err, app.ErrInvalid, "project of another organization")

	epic := mk(t, tr, tracker.KindEpic, "Billing")
	_, err = cs.Propose(bob, app.ProposeInput{ProjectKey: "WEB", Title: "x", Ops: []changeset.Op{
		{Kind: changeset.OpUpdateItem, Update: &changeset.UpdateItem{Item: epic.Key, Features: &[]string{"F-1"}}},
	}})
	assert.ErrorIs(t, err, app.ErrInvalid, "only tickets change features")

	proposed, err := cs.Propose(bob, exportPlan("F-1"))
	require.NoError(t, err)
	_, err = fs.Update(bob, "acme", "F-1", app.UpdateFeatureInput{Version: 1, Title: ptr("Exports")})
	require.NoError(t, err)
	applied, err := cs.Apply(bob, proposed.ID, []int{0, 1, 2, 3})
	require.NoError(t, err, "a concurrent edit is retried against the new version")
	assert.Equal(t, "F-1", applied.Results[1].Key)
	f, err := fs.Get(bob, "acme", "F-1")
	require.NoError(t, err)
	assert.Equal(t, "Exports", f.Title, "the concurrent rename is kept")
	assert.Equal(t, int64(3), f.Version)
}

func TestFeatures_StatusFollowsTickets(t *testing.T) {
	cs, tr, fs := newFeatureChangesets(t)
	bob := user(t, "bob", "acme-devs")
	_, err := fs.Create(bob, "acme", app.CreateFeatureInput{Title: "Invoice export"})
	require.NoError(t, err)
	proposed, err := cs.Propose(bob, exportPlan("F-1"))
	require.NoError(t, err)
	applied, err := cs.Apply(bob, proposed.ID, []int{0, 1, 2, 3})
	require.NoError(t, err)
	ticket := applied.Results[3].Key

	n, err := fs.SyncStatus(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n, "backlog tickets change nothing")

	it, err := tr.GetItem(bob, ticket)
	require.NoError(t, err)
	it, err = tr.TransitionItem(bob, ticket, tracker.StateReady, it.Version)
	require.NoError(t, err)
	_, err = tr.TransitionItem(bob, ticket, tracker.StatePaused, it.Version) // started, then held
	require.NoError(t, err)
	n, err = fs.SyncStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	f, err := fs.Get(bob, "acme", "F-2")
	require.NoError(t, err)
	assert.Equal(t, feature.StatusInProgress, f.Status)

	it, err = tr.GetItem(bob, ticket)
	require.NoError(t, err)
	_, err = tr.TransitionItem(bob, ticket, tracker.StateDone, it.Version)
	require.NoError(t, err)
	_, err = fs.SyncStatus(t.Context())
	require.NoError(t, err)
	f, err = fs.Get(bob, "acme", "F-2")
	require.NoError(t, err)
	assert.Equal(t, feature.StatusLive, f.Status)
	revs, err := fs.Revisions(bob, "acme", "F-2")
	require.NoError(t, err)
	assert.Equal(t, "Delivered: "+ticket, revs[0].Reason)
	assert.Equal(t, "ballet", revs[0].Author.Subject)
	assert.Equal(t, feature.ReviewNone, revs[0].Review, "Ballet's own revisions need no review")

	n, err = fs.SyncStatus(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n, "idempotent")
}
