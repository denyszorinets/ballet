package app_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

func mk(t *testing.T, tr *app.Tracker, kind tracker.Kind, title string) app.ItemView {
	t.Helper()
	it, err := tr.CreateItem(user(t, "bob", "acme-devs"), app.CreateItemInput{ProjectKey: "WEB", Kind: kind, Title: title})
	require.NoError(t, err)
	return it
}

func ready(t *testing.T, tr *app.Tracker, it app.ItemView) app.ItemView {
	t.Helper()
	got, err := tr.TransitionItem(user(t, "bob", "acme-devs"), it.Key, tracker.StateReady, it.Version)
	require.NoError(t, err)
	return got
}

func runnableKeys(t *testing.T, tr *app.Tracker) []string {
	t.Helper()
	items, err := tr.Runnable(user(t, "bob", "acme-devs"), "WEB")
	require.NoError(t, err)
	keys := []string{}
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	return keys
}

func TestDependencies_BlockersGateReadiness(t *testing.T) {
	tr, _ := newTracker(t)
	bob := user(t, "bob", "acme-devs")
	a := ready(t, tr, mk(t, tr, tracker.KindTicket, "schema"))
	b := ready(t, tr, mk(t, tr, tracker.KindTicket, "api"))
	c := ready(t, tr, mk(t, tr, tracker.KindTicket, "ui"))

	_, err := tr.AddDependency(bob, a.Key, app.DirBlocks, b.Key)
	require.NoError(t, err)
	_, err = tr.AddDependency(bob, c.Key, app.DirBlockedBy, b.Key)
	require.NoError(t, err)
	assert.Equal(t, []string{a.Key}, runnableKeys(t, tr), "only the unblocked ticket can run")

	_, err = tr.TransitionItem(bob, a.Key, tracker.StateDone, a.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{b.Key}, runnableKeys(t, tr), "b is unblocked once a is done")

	_, err = tr.TransitionItem(bob, b.Key, tracker.StateCancelled, b.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{c.Key}, runnableKeys(t, tr), "cancelled blockers release dependents")
}

func TestDependencies_CrossLevelBlockingAndCycles(t *testing.T) {
	tr, _ := newTracker(t)
	bob := user(t, "bob", "acme-devs")
	m := mk(t, tr, tracker.KindMilestone, "MVP")
	tk := ready(t, tr, mk(t, tr, tracker.KindTicket, "after MVP"))
	pre := mk(t, tr, tracker.KindTicket, "before MVP")

	_, err := tr.AddDependency(bob, m.Key, app.DirBlocks, tk.Key)
	require.NoError(t, err, "milestone blocks ticket")
	_, err = tr.AddDependency(bob, pre.Key, app.DirBlocks, m.Key)
	require.NoError(t, err, "ticket blocks milestone")
	assert.Empty(t, runnableKeys(t, tr), "ticket waits for the open milestone")

	_, err = tr.AddDependency(bob, tk.Key, app.DirBlocks, pre.Key)
	assert.ErrorIs(t, err, app.ErrInvalid, "tk → pre would close the cycle pre → m → tk")
	assert.ErrorContains(t, err, "cycle")
	_, err = tr.AddDependency(bob, tk.Key, app.DirBlocks, tk.Key)
	assert.ErrorIs(t, err, app.ErrInvalid)
}

func TestDependencies_ListFromEachSideAndRemove(t *testing.T) {
	tr, env := newTracker(t)
	bob := user(t, "bob", "acme-devs")
	a := mk(t, tr, tracker.KindTicket, "a")
	b := mk(t, tr, tracker.KindTicket, "b")
	c := mk(t, tr, tracker.KindTicket, "c")
	edge, err := tr.AddDependency(bob, a.Key, app.DirBlocks, b.Key)
	require.NoError(t, err)
	_, err = tr.AddDependency(bob, c.Key, app.DirRelates, a.Key)
	require.NoError(t, err)

	_, err = tr.AddDependency(bob, a.Key, app.DirRelates, c.Key)
	assert.ErrorIs(t, err, app.ErrAlreadyExists, "relates is undirected")
	_, err = tr.AddDependency(bob, b.Key, app.DirBlockedBy, a.Key)
	assert.ErrorIs(t, err, app.ErrAlreadyExists)

	fromA, err := tr.Dependencies(bob, a.Key)
	require.NoError(t, err)
	got := map[app.Direction]string{}
	for _, d := range fromA {
		got[d.Direction] = d.Other.Key
	}
	assert.Equal(t, map[app.Direction]string{app.DirBlocks: b.Key, app.DirRelates: c.Key}, got)

	fromB, err := tr.Dependencies(bob, b.Key)
	require.NoError(t, err)
	require.Len(t, fromB, 1)
	assert.Equal(t, app.DirBlockedBy, fromB[0].Direction)
	assert.Equal(t, a.Key, fromB[0].Other.Key)

	require.NoError(t, tr.RemoveDependency(bob, edge.ID))
	fromB, err = tr.Dependencies(bob, b.Key)
	require.NoError(t, err)
	assert.Empty(t, fromB)
	assert.ErrorIs(t, tr.RemoveDependency(bob, edge.ID), app.ErrNotFound)

	events, err := env.store.ListEvents(t.Context(), storeFilterDeps())
	require.NoError(t, err)
	assert.Len(t, events, 3, "added, added, removed")
}

func TestDependencies_RejectsCrossProjectAndUnauthorized(t *testing.T) {
	tr, _ := newTracker(t)
	bob := user(t, "bob", "acme-devs")
	a := mk(t, tr, tracker.KindTicket, "a")
	other, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "APP", Kind: tracker.KindTicket, Title: "x"})
	require.NoError(t, err)

	_, err = tr.AddDependency(bob, a.Key, app.DirBlocks, other.Key)
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = tr.AddDependency(bob, a.Key, "needs", other.Key)
	assert.ErrorIs(t, err, app.ErrInvalid)
	b := mk(t, tr, tracker.KindTicket, "b")
	_, err = tr.AddDependency(user(t, "carol", "acme-viewers"), a.Key, app.DirBlocks, b.Key)
	assert.ErrorIs(t, err, app.ErrForbidden)
}

func TestDependencies_ConcurrentOppositeEdgesCannotFormACycle(t *testing.T) {
	for round := range 5 {
		tr, _ := newTracker(t)
		bob := user(t, "bob", "acme-devs")
		a := mk(t, tr, tracker.KindTicket, "a")
		b := mk(t, tr, tracker.KindTicket, "b")

		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Go(func() { _, errs[0] = tr.AddDependency(bob, a.Key, app.DirBlocks, b.Key) })
		wg.Go(func() { _, errs[1] = tr.AddDependency(bob, b.Key, app.DirBlocks, a.Key) })
		wg.Wait()

		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else {
				assert.ErrorIs(t, err, app.ErrInvalid, "round %d: loser sees the cycle", round)
			}
		}
		assert.Equal(t, 1, ok, "round %d: exactly one edge wins", round)
	}
}

func storeFilterDeps() store.EventFilter { return store.EventFilter{EntityType: "dependency"} }
