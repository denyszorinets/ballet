package app_test

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

func newTracker(t *testing.T) (*app.Tracker, rbacEnv) {
	t.Helper()
	env := newRBACEnv(t)
	seed(t, env)
	return &app.Tracker{
		Items: env.store, Tenancy: env.store, Events: env.store, Authz: env.rbac,
		Now: time.Now, NewID: store.NewID,
	}, env
}

func ptr[T any](v T) *T { return &v }

func TestTracker_CreatesItemsWithSharedKeySequence(t *testing.T) {
	tr, _ := newTracker(t)
	bob := user(t, "bob", "acme-devs")

	m, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindMilestone, Title: "MVP"})
	require.NoError(t, err)
	e, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindEpic, Title: "Billing", MilestoneKey: m.Key})
	require.NoError(t, err)
	tk, err := tr.CreateItem(bob, app.CreateItemInput{
		ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Export invoices", EpicKey: e.Key, MilestoneKey: m.Key,
		AcceptanceCriteria: []string{"CSV export"},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"WEB-1", "WEB-2", "WEB-3"}, []string{m.Key, e.Key, tk.Key})
	assert.Equal(t, tracker.StateOpen, m.State)
	assert.Equal(t, tracker.StateBacklog, tk.State)
	assert.Equal(t, tracker.TypeFeature, tk.Type, "default type")
	assert.Equal(t, tracker.DefaultPolicy, tk.Policy, "default policy is autonomous")
	assert.Equal(t, "WEB-2", tk.EpicKey)
	assert.Equal(t, "WEB-1", tk.MilestoneKey)
	assert.Equal(t, "acme", tk.CustomerKey)

	other, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "APP", Kind: tracker.KindTicket, Title: "x"})
	require.NoError(t, err)
	assert.Equal(t, "APP-1", other.Key, "sequences are per project")
}

func TestTracker_ConcurrentCreatesGetUniqueSequentialKeys(t *testing.T) {
	tr, _ := newTracker(t)
	bob := user(t, "bob", "acme-devs")

	const n = 10
	var wg sync.WaitGroup
	numbers := make([]int64, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			it, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "t"})
			numbers[i], errs[i] = it.Number, err
		})
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	sort.Slice(numbers, func(a, b int) bool { return numbers[a] < numbers[b] })
	for i, num := range numbers {
		assert.Equal(t, int64(i+1), num)
	}
}

func TestTracker_RejectsInvalidRelationsAndContent(t *testing.T) {
	tr, _ := newTracker(t)
	bob := user(t, "bob", "acme-devs")
	ticket, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "t"})
	require.NoError(t, err)
	otherEpic, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "APP", Kind: tracker.KindEpic, Title: "e"})
	require.NoError(t, err)

	for name, in := range map[string]app.CreateItemInput{
		"epic is a ticket":      {ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "x", EpicKey: ticket.Key},
		"epic of other project": {ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "x", EpicKey: otherEpic.Key},
		"unknown epic":          {ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "x", EpicKey: "WEB-99"},
		"epic on an epic":       {ProjectKey: "WEB", Kind: tracker.KindEpic, Title: "x", EpicKey: otherEpic.Key},
		"empty title":           {ProjectKey: "WEB", Kind: tracker.KindTicket, Title: ""},
		"unknown kind":          {ProjectKey: "WEB", Kind: "story", Title: "x"},
		"bad policy": {ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "x",
			Policy: &tracker.Policy{ReviewMode: "none", MergeMode: tracker.MergeAuto}},
	} {
		_, err := tr.CreateItem(bob, in)
		assert.ErrorIs(t, err, app.ErrInvalid, name)
	}
	_, err = tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "NOPE", Kind: tracker.KindTicket, Title: "x"})
	assert.ErrorIs(t, err, app.ErrNotFound)
}

func TestTracker_UpdateAndTransition(t *testing.T) {
	tr, env := newTracker(t)
	bob := user(t, "bob", "acme-devs")
	tk, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Export"})
	require.NoError(t, err)

	tk, err = tr.UpdateItem(bob, app.UpdateItemInput{
		Key: tk.Key, Version: tk.Version, Title: ptr("Export invoices"),
		AcceptanceCriteria: ptr([]string{"one row per invoice"}),
		Policy:             &tracker.Policy{ReviewMode: tracker.ReviewAgentHuman, MergeMode: tracker.MergeManual},
	})
	require.NoError(t, err)
	assert.Equal(t, "Export invoices", tk.Title)
	assert.Equal(t, tracker.MergeManual, tk.Policy.MergeMode)
	assert.Equal(t, int64(2), tk.Version)

	_, err = tr.UpdateItem(bob, app.UpdateItemInput{Key: tk.Key, Version: 1, Title: ptr("stale")})
	assert.ErrorIs(t, err, app.ErrConflict)
	_, err = tr.UpdateItem(bob, app.UpdateItemInput{Key: tk.Key, Version: tk.Version})
	assert.ErrorIs(t, err, app.ErrInvalid, "nothing to update")

	tk, err = tr.TransitionItem(bob, tk.Key, tracker.StateReady, tk.Version)
	require.NoError(t, err)
	assert.Equal(t, tracker.StateReady, tk.State)
	_, err = tr.TransitionItem(bob, tk.Key, tracker.StateInProgress, tk.Version)
	assert.ErrorIs(t, err, app.ErrInvalid, "in_progress is set by the orchestrator")

	history, err := tr.ItemHistory(bob, tk.Key)
	require.NoError(t, err)
	var types []string
	for _, e := range history {
		types = append(types, e.Type)
	}
	assert.Equal(t, []string{"item.created", "item.updated", "item.state_changed"}, types)
	assert.Equal(t, "bob", history[2].Actor.Subject)

	all, err := env.store.ListEvents(t.Context(), store.EventFilter{EntityType: "item"})
	require.NoError(t, err)
	assert.Len(t, all, 3, "exactly one event per mutation")
}

func TestTracker_ListFilters(t *testing.T) {
	tr, _ := newTracker(t)
	bob := user(t, "bob", "acme-devs")
	epic, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindEpic, Title: "E"})
	require.NoError(t, err)
	_, err = tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "in epic", EpicKey: epic.Key})
	require.NoError(t, err)
	loose, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "loose"})
	require.NoError(t, err)
	_, err = tr.TransitionItem(bob, loose.Key, tracker.StateReady, loose.Version)
	require.NoError(t, err)

	all, err := tr.ListItems(bob, "WEB", "", "", "", "")
	require.NoError(t, err)
	assert.Len(t, all, 3)
	tickets, err := tr.ListItems(bob, "WEB", tracker.KindTicket, "", "", "")
	require.NoError(t, err)
	assert.Len(t, tickets, 2)
	inEpic, err := tr.ListItems(bob, "WEB", "", "", epic.Key, "")
	require.NoError(t, err)
	require.Len(t, inEpic, 1)
	assert.Equal(t, epic.Key, inEpic[0].EpicKey)
	ready, err := tr.ListItems(bob, "WEB", "", tracker.StateReady, "", "")
	require.NoError(t, err)
	assert.Len(t, ready, 1)
}

func TestTracker_Authorization(t *testing.T) {
	tr, _ := newTracker(t)
	bob := user(t, "bob", "acme-devs")
	carol := user(t, "carol", "acme-viewers")
	tk, err := tr.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "t"})
	require.NoError(t, err)

	_, err = tr.GetItem(carol, tk.Key)
	assert.NoError(t, err, "viewers read")
	_, err = tr.CreateItem(carol, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "t"})
	assert.ErrorIs(t, err, app.ErrForbidden, "viewers do not write")
	_, err = tr.TransitionItem(carol, tk.Key, tracker.StateReady, tk.Version)
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = tr.ListItems(carol, "APP", "", "", "", "")
	assert.ErrorIs(t, err, app.ErrForbidden, "carol only sees project WEB")
	_, err = tr.GetItem(user(t, "eve"), tk.Key)
	assert.ErrorIs(t, err, app.ErrForbidden)
}
