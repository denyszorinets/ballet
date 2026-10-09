package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/kit/embed"
)

func refs(rs []app.SearchResult) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Ref)
	}
	return out
}

func TestSearch_IndexesItemsAndSkillsAndFiltersByPermission(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	tr := &app.Tracker{Items: env.store, Deps: env.store, Tenancy: env.store, Events: env.store, Authz: env.rbac, Now: time.Now, NewID: store.NewID}
	sk := &app.Skills{Store: env.store, Tenancy: env.store, Authz: env.rbac, Now: time.Now, NewID: store.NewID}
	alice := user(t, "alice", "ballet-admins")
	mk := func(project, title string) app.ItemView {
		it, err := tr.CreateItem(alice, app.CreateItemInput{ProjectKey: project, Kind: tracker.KindTicket, Title: title,
			AcceptanceCriteria: []string{"invoices exported as csv"}})
		require.NoError(t, err)
		return it
	}
	web := mk("WEB", "Invoice export")
	mk("APP", "Invoice export in the app")
	mk("GLX", "Globex invoice export")
	_, err := sk.CreateSkill(alice, "platform", "csv-exports", content("How to build CSV exports of invoices", "Use RFC 4180."))
	require.NoError(t, err)
	_, err = sk.CreateSkill(alice, "organization:globex", "globex-invoices", content("Globex invoice rules", "invoice csv"))
	require.NoError(t, err)

	ix := &app.SearchIndexer{Store: env.store, Log: env.store, Items: env.store, Skills: env.store, Tenancy: env.store, Embedder: embed.Hash{}}
	require.NoError(t, ix.IndexOnce(t.Context()))
	search := &app.Search{Store: env.store, Tenancy: env.store, Authz: env.rbac, Embedder: embed.Hash{}}

	all, err := search.Query(alice, "invoice csv export", "", "", 0)
	require.NoError(t, err)
	assert.Len(t, all, 5, "admins see everything")

	bob, err := search.Query(user(t, "bob", "acme-devs"), "invoice csv export", "", "", 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"WEB-1", "APP-1"}, refs(bob),
		"acme engineers see acme items only; platform and globex skills need skill.read there")
	assert.NotContains(t, refs(bob), "GLX-1")

	carol, err := search.Query(user(t, "carol", "acme-viewers"), "invoice csv export", "", "", 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"WEB-1"}, refs(carol), "project viewers see their project only")

	skills, err := search.Query(alice, "invoice csv", "skill", "", 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"csv-exports", "globex-invoices"}, refs(skills))
	inWeb, err := search.Query(alice, "invoice csv export", "", "WEB", 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"WEB-1", "csv-exports"}, refs(inWeb), "project filter: its items plus its skill chain")

	// Incremental: a changed title is re-indexed from the next events.
	title := "Payment reminders"
	_, err = tr.UpdateItem(alice, app.UpdateItemInput{Key: web.Key, Version: web.Version, Title: &title})
	require.NoError(t, err)
	require.NoError(t, ix.IndexOnce(t.Context()))
	got, err := search.Query(alice, "payment reminders", "item", "", 0)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, "WEB-1", got[0].Ref)
	cursor, err := env.store.SearchCursor(t.Context())
	require.NoError(t, err)
	last, err := env.store.LastEventSeq(t.Context())
	require.NoError(t, err)
	assert.Equal(t, last, cursor, "cursor persisted at the end of the log")
}
