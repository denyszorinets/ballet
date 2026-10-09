package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
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
