package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/kit/auth"
)

// clock is a settable time source.
type clock struct{ t time.Time }

func (c *clock) Now() time.Time          { return c.t }
func (c *clock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newFeatures(t *testing.T) (*app.Features, *clock) {
	t.Helper()
	env := newRBACEnv(t)
	seed(t, env)
	c := &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	return &app.Features{Store: env.store, Tenancy: env.store, Authz: env.rbac, Now: c.Now, NewID: store.NewID}, c
}

func TestFeatures_CreateAssignsKeysAndRecordsRevisions(t *testing.T) {
	fs, _ := newFeatures(t)
	bob := user(t, "bob", "acme-devs")

	a, err := fs.Create(bob, "acme", app.CreateFeatureInput{
		Title: "Invoice export", Description: "CSV export of invoices.", ProjectKeys: []string{"WEB", "APP"},
		Reason: "Customers asked for it",
	})
	require.NoError(t, err)
	b, err := fs.Create(bob, "acme", app.CreateFeatureInput{Title: "Invoice import"})
	require.NoError(t, err)

	assert.Equal(t, "F-1", a.Key)
	assert.Equal(t, "F-2", b.Key)
	assert.Equal(t, feature.StatusPlanned, a.Status, "default status")
	assert.Equal(t, []string{"APP", "WEB"}, a.ProjectKeys, "sorted project keys")
	assert.Equal(t, int64(1), a.Version)

	revs, err := fs.Revisions(bob, "acme", "F-1")
	require.NoError(t, err)
	require.Len(t, revs, 1)
	assert.Equal(t, "Customers asked for it", revs[0].Reason)
	assert.Equal(t, "bob", revs[0].Author.Subject)
	assert.Equal(t, feature.ReviewNone, revs[0].Review, "human revisions need no review")

	_, err = fs.Create(bob, "acme", app.CreateFeatureInput{Title: "x", ProjectKeys: []string{"GLX"}})
	assert.ErrorIs(t, err, app.ErrInvalid, "projects of another organization")
}

func TestFeatures_UpdateAppendsRevisionsAndGuardsVersion(t *testing.T) {
	fs, c := newFeatures(t)
	bob := user(t, "bob", "acme-devs")
	f, err := fs.Create(bob, "acme", app.CreateFeatureInput{Title: "Invoice export", Description: "CSV."})
	require.NoError(t, err)

	c.Advance(time.Hour)
	f, err = fs.Update(bob, "acme", "F-1", app.UpdateFeatureInput{
		Version: 1, Description: ptr("CSV and PDF."), Status: ptr(feature.StatusLive), Reason: "PDF added",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), f.Version)
	assert.Equal(t, "CSV and PDF.", f.Description)

	_, err = fs.Update(bob, "acme", "F-1", app.UpdateFeatureInput{Version: 1, Title: ptr("Stale")})
	assert.ErrorIs(t, err, app.ErrConflict)
	_, err = fs.Update(bob, "acme", "F-1", app.UpdateFeatureInput{Version: 2})
	assert.ErrorIs(t, err, app.ErrInvalid, "nothing to change")

	revs, err := fs.Revisions(bob, "acme", "F-1")
	require.NoError(t, err)
	require.Len(t, revs, 2)
	assert.Equal(t, int64(2), revs[0].Number, "newest first")
	assert.Equal(t, "CSV.", revs[1].Description)
	assert.Equal(t, "PDF added", revs[0].Reason)
}

func TestFeatures_GraphAtShowsTheMapAsItWas(t *testing.T) {
	fs, c := newFeatures(t)
	bob := user(t, "bob", "acme-devs")
	t0 := c.Now()
	_, err := fs.Create(bob, "acme", app.CreateFeatureInput{Title: "Export", ProjectKeys: []string{"WEB"}})
	require.NoError(t, err)

	c.Advance(time.Hour) // t0+1h
	_, err = fs.Create(bob, "acme", app.CreateFeatureInput{Title: "Scheduled export", ProjectKeys: []string{"APP"}})
	require.NoError(t, err)
	l, err := fs.Link(bob, "acme", app.LinkFeaturesInput{From: "F-2", To: "F-1", Type: feature.LinkDerivedFrom})
	require.NoError(t, err)

	c.Advance(time.Hour) // t0+2h
	_, err = fs.Update(bob, "acme", "F-1", app.UpdateFeatureInput{Version: 1, Title: ptr("Invoice export")})
	require.NoError(t, err)

	c.Advance(time.Hour) // t0+3h
	require.NoError(t, fs.Unlink(bob, "acme", l.ID))

	at := func(d time.Duration) app.FeatureGraph {
		g, err := fs.Graph(bob, "acme", app.GraphQuery{At: t0.Add(d)})
		require.NoError(t, err)
		return g
	}
	g := at(30 * time.Minute)
	require.Len(t, g.Features, 1)
	assert.Equal(t, "Export", g.Features[0].Title)
	assert.Empty(t, g.Links)

	g = at(90 * time.Minute)
	require.Len(t, g.Features, 2)
	require.Len(t, g.Links, 1)
	assert.Equal(t, "F-2", g.Links[0].FromKey)
	assert.Equal(t, "Export", g.Features[0].Title, "renamed later")

	g = at(150 * time.Minute)
	assert.Equal(t, "Invoice export", g.Features[0].Title)
	assert.Len(t, g.Links, 1)

	g = at(4 * time.Hour)
	assert.Empty(t, g.Links, "removed link")
	assert.Len(t, g.Changes, 5, "two creations, a link, a rename and an unlink")

	g, err = fs.Graph(bob, "acme", app.GraphQuery{ProjectKey: "APP"})
	require.NoError(t, err)
	require.Len(t, g.Features, 1, "filtered by project")
	assert.Equal(t, "F-2", g.Features[0].Key)
}

func TestFeatures_LinksAreValidated(t *testing.T) {
	fs, _ := newFeatures(t)
	bob := user(t, "bob", "acme-devs")
	for _, title := range []string{"A", "B"} {
		_, err := fs.Create(bob, "acme", app.CreateFeatureInput{Title: title})
		require.NoError(t, err)
	}
	_, err := fs.Link(bob, "acme", app.LinkFeaturesInput{From: "F-1", To: "F-1", Type: feature.LinkRelates})
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = fs.Link(bob, "acme", app.LinkFeaturesInput{From: "F-1", To: "F-9", Type: feature.LinkRelates})
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = fs.Link(bob, "acme", app.LinkFeaturesInput{From: "F-1", To: "F-2", Type: feature.LinkDependsOn})
	require.NoError(t, err)
	_, err = fs.Link(bob, "acme", app.LinkFeaturesInput{From: "F-1", To: "F-2", Type: feature.LinkDependsOn})
	assert.ErrorIs(t, err, app.ErrInvalid, "duplicate active link")

	d, err := fs.Get(bob, "acme", "F-1")
	require.NoError(t, err)
	require.Len(t, d.Links, 1)
	assert.Equal(t, "F-2", d.Links[0].ToKey)
}

func TestFeatures_AuthorizationFollowsOrganizationAndProjects(t *testing.T) {
	fs, _ := newFeatures(t)
	bob := user(t, "bob", "acme-devs")
	carol := user(t, "carol", "acme-viewers") // viewer of project WEB only
	dave := user(t, "dave")                   // no bindings
	_, err := fs.Create(bob, "acme", app.CreateFeatureInput{Title: "Web only", ProjectKeys: []string{"WEB"}})
	require.NoError(t, err)
	_, err = fs.Create(bob, "acme", app.CreateFeatureInput{Title: "App only", ProjectKeys: []string{"APP"}})
	require.NoError(t, err)
	_, err = fs.Create(bob, "acme", app.CreateFeatureInput{Title: "No project"})
	require.NoError(t, err)

	all, err := fs.List(bob, "acme", app.FeatureFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 3)

	visible, err := fs.List(carol, "acme", app.FeatureFilter{})
	require.NoError(t, err)
	require.Len(t, visible, 1, "project readers see the features of their projects")
	assert.Equal(t, "Web only", visible[0].Title)
	_, err = fs.Get(carol, "acme", "F-2")
	assert.ErrorIs(t, err, app.ErrNotFound)
	_, err = fs.Update(carol, "acme", "F-1", app.UpdateFeatureInput{Version: 1, Title: ptr("x")})
	assert.ErrorIs(t, err, app.ErrForbidden, "viewers cannot write")

	_, err = fs.List(dave, "acme", app.FeatureFilter{})
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = fs.List(bob, "globex", app.FeatureFilter{})
	assert.ErrorIs(t, err, app.ErrForbidden, "other organization")
}

// allowAgents authorizes everything (agents are authorized by their run
// tokens, outside role bindings).
type allowAgents struct{}

func (allowAgents) Authorize(context.Context, auth.Identity, app.Action, app.Scope) error { return nil }

func agentRun(t *testing.T) context.Context {
	return auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindService, Subject: "run:r1"})
}

func TestFeatures_PolicyInheritsFromOrganization(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	fs := &app.Features{Store: env.store, Tenancy: env.store, Execution: env.store, Authz: env.rbac, Now: time.Now, NewID: store.NewID}
	alice := user(t, "alice", "ballet-admins")
	org, err := env.store.OrganizationByKey(t.Context(), "acme")
	require.NoError(t, err)
	web, err := env.store.ProjectByKey(t.Context(), "WEB")
	require.NoError(t, err)

	p, err := fs.Policy(t.Context(), org, web.ID)
	require.NoError(t, err)
	assert.Equal(t, feature.PolicyDirect, p, "default")

	org, err = env.tenancy.UpdateOrganization(alice, app.UpdateOrganizationInput{Key: "acme", FeaturePolicy: "proposal", Version: org.Version})
	require.NoError(t, err)
	p, err = fs.Policy(t.Context(), org, web.ID)
	require.NoError(t, err)
	assert.Equal(t, feature.PolicyProposal, p, "organization")

	ex := &app.Execution{Store: env.store, Tenancy: env.store, Authz: env.rbac, Now: time.Now}
	_, err = ex.Set(alice, "WEB", execution.Settings{FeaturePolicy: "read_only"}, 0)
	require.NoError(t, err)
	p, err = fs.Policy(t.Context(), org, web.ID)
	require.NoError(t, err)
	assert.Equal(t, feature.PolicyReadOnly, p, "project override")

	_, err = ex.Set(alice, "APP", execution.Settings{FeaturePolicy: "sometimes"}, 0)
	assert.ErrorIs(t, err, app.ErrInvalid)
}

func TestFeatures_AgentRevisionsWaitForReview(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	c := &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	agents := &app.Features{Store: env.store, Tenancy: env.store, Authz: allowAgents{}, Now: c.Now, NewID: store.NewID}
	humans := &app.Features{Store: env.store, Tenancy: env.store, Authz: env.rbac, Now: c.Now, NewID: store.NewID}
	bob := user(t, "bob", "acme-devs")

	_, err := humans.Create(bob, "acme", app.CreateFeatureInput{Title: "Export", Description: "CSV."})
	require.NoError(t, err)
	c.Advance(time.Minute)
	_, err = agents.Update(agentRun(t), "acme", "F-1", app.UpdateFeatureInput{Version: 1, Description: ptr("CSV, PDF."), Reason: "Added PDF"})
	require.NoError(t, err)
	c.Advance(time.Minute)
	_, err = agents.Create(agentRun(t), "acme", app.CreateFeatureInput{Title: "Export scheduling"})
	require.NoError(t, err)

	queue, err := humans.Reviews(bob, "acme")
	require.NoError(t, err)
	require.Len(t, queue, 2, "the agent's update and creation")
	assert.Equal(t, "F-1", queue[0].FeatureKey, "oldest first")
	assert.Equal(t, int64(2), queue[0].Number)
	assert.Equal(t, "Export", queue[0].FeatureTitle)
	assert.Equal(t, "CSV.", queue[0].Previous.Description, "with the state before, for a diff")

	_, err = humans.Review(agentRun(t), "acme", "F-1", 2, app.ReviewInput{Action: "confirm"})
	assert.ErrorIs(t, err, app.ErrForbidden, "only humans review")

	r, err := humans.Review(bob, "acme", "F-1", 2, app.ReviewInput{Action: "confirm"})
	require.NoError(t, err)
	assert.Equal(t, feature.ReviewConfirmed, r.Review)
	assert.Equal(t, "bob", r.ReviewedBy)
	_, err = humans.Review(bob, "acme", "F-1", 2, app.ReviewInput{Action: "revert"})
	assert.ErrorIs(t, err, app.ErrConflict, "already reviewed")

	c.Advance(time.Minute)
	r, err = humans.Review(bob, "acme", "F-2", 1, app.ReviewInput{Action: "revert", Comment: "Not a feature"})
	require.NoError(t, err)
	assert.Equal(t, feature.ReviewReverted, r.Review)
	f, err := humans.Get(bob, "acme", "F-2")
	require.NoError(t, err)
	assert.Equal(t, feature.StatusRemoved, f.Status, "reverting a creation removes the feature")
	revs, err := humans.Revisions(bob, "acme", "F-2")
	require.NoError(t, err)
	require.Len(t, revs, 2)
	assert.Equal(t, "Reverted revision 1: Not a feature", revs[0].Reason)
	assert.Equal(t, feature.Cause{Kind: "revert", Ref: "1"}, revs[0].Cause)

	queue, err = humans.Reviews(bob, "acme")
	require.NoError(t, err)
	assert.Empty(t, queue)
}

func TestFeatures_RevertRestoresThePreviousRevision(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	agents := &app.Features{Store: env.store, Tenancy: env.store, Authz: allowAgents{}, Now: time.Now, NewID: store.NewID}
	humans := &app.Features{Store: env.store, Tenancy: env.store, Authz: env.rbac, Now: time.Now, NewID: store.NewID}
	bob := user(t, "bob", "acme-devs")
	_, err := humans.Create(bob, "acme", app.CreateFeatureInput{Title: "Export", Description: "CSV.", ProjectKeys: []string{"WEB"}})
	require.NoError(t, err)
	_, err = agents.Update(agentRun(t), "acme", "F-1", app.UpdateFeatureInput{Version: 1, Title: ptr("Exports"),
		Status: ptr(feature.StatusDeprecated), ProjectKeys: &[]string{"APP"}})
	require.NoError(t, err)
	_, err = humans.Update(bob, "acme", "F-1", app.UpdateFeatureInput{Version: 2, Description: ptr("CSV only.")})
	require.NoError(t, err)

	_, err = humans.Review(bob, "acme", "F-1", 2, app.ReviewInput{Action: "revert"})
	assert.ErrorIs(t, err, app.ErrConflict, "later revisions exist")
	_, err = humans.Review(bob, "acme", "F-1", 1, app.ReviewInput{Action: "confirm"})
	assert.ErrorIs(t, err, app.ErrConflict, "human revisions need no review")
	_, err = humans.Review(bob, "acme", "F-1", 2, app.ReviewInput{Action: "maybe"})
	assert.ErrorIs(t, err, app.ErrInvalid)

	_, err = humans.Update(bob, "acme", "F-1", app.UpdateFeatureInput{Version: 3, Description: ptr("CSV.")})
	require.NoError(t, err)
	_, err = agents.Update(agentRun(t), "acme", "F-1", app.UpdateFeatureInput{Version: 4, Title: ptr("Data exports")})
	require.NoError(t, err)
	_, err = humans.Review(bob, "acme", "F-1", 5, app.ReviewInput{Action: "revert"})
	require.NoError(t, err)
	f, err := humans.Get(bob, "acme", "F-1")
	require.NoError(t, err)
	assert.Equal(t, "Exports", f.Title, "restored revision 4")
	assert.Equal(t, int64(6), f.Version)
}
