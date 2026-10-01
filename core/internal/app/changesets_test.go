package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/auth"
)

func newChangesets(t *testing.T) (*app.Changesets, *app.Tracker) {
	t.Helper()
	tr, env := newTracker(t)
	return &app.Changesets{Store: env.store, Tracker: tr}, tr
}

func createOp(ref string, kind tracker.Kind, title string) changeset.Op {
	return changeset.Op{Kind: changeset.OpCreateItem, Ref: ref, Create: &changeset.CreateItem{Kind: kind, Title: title}}
}

func blocks(from, to string) changeset.Op {
	return changeset.Op{Kind: changeset.OpAddDependency, Dependency: &changeset.AddDependency{From: from, To: to, Type: tracker.DepBlocks}}
}

// authPlan: epic, two tickets in it, login blocks logout, and an update of
// an existing ticket that the new login ticket blocks.
func authPlan(existing string) app.ProposeInput {
	login := createOp("login", tracker.KindTicket, "Login")
	login.Create.Epic = "$auth"
	login.Create.AcceptanceCriteria = []string{"OIDC"}
	logout := createOp("logout", tracker.KindTicket, "Logout")
	logout.Create.Epic = "$auth"
	return app.ProposeInput{ProjectKey: "WEB", Title: "Authentication", Summary: "Why: users need accounts.", Ops: []changeset.Op{
		createOp("auth", tracker.KindEpic, "Auth"),
		login,
		logout,
		blocks("$login", "$logout"),
		{Kind: changeset.OpUpdateItem, Update: &changeset.UpdateItem{Item: existing, Title: ptr("Profile page (needs login)")}},
		blocks("$login", existing),
	}}
}

func TestChangesets_ApplyAllCreatesExactlyTheProposedPlan(t *testing.T) {
	cs, tr := newChangesets(t)
	bob := user(t, "bob", "acme-devs")
	profile := mk(t, tr, tracker.KindTicket, "Profile page")

	proposed, err := cs.Propose(bob, authPlan(profile.Key))
	require.NoError(t, err)
	assert.Equal(t, changeset.StatusProposed, proposed.Status)
	before, _ := tr.ListItems(bob, "WEB", "", "", "", "")
	assert.Len(t, before, 1, "proposing changes nothing")

	applied, err := cs.Apply(bob, proposed.ID, []int{0, 1, 2, 3, 4, 5})
	require.NoError(t, err)
	assert.Equal(t, changeset.StatusApplied, applied.Status)
	assert.Equal(t, "bob", applied.DecidedBy.Subject)
	keys := []string{applied.Results[0].Key, applied.Results[1].Key, applied.Results[2].Key}
	assert.Equal(t, []string{"WEB-2", "WEB-3", "WEB-4"}, keys)
	assert.Equal(t, profile.Key, applied.Results[4].Key)
	assert.NotEmpty(t, applied.Results[3].DependencyID)

	login, err := tr.GetItem(bob, "WEB-3")
	require.NoError(t, err)
	assert.Equal(t, "Login", login.Title)
	assert.Equal(t, "WEB-2", login.EpicKey)
	assert.Equal(t, []string{"OIDC"}, login.AcceptanceCriteria)
	updated, _ := tr.GetItem(bob, profile.Key)
	assert.Equal(t, "Profile page (needs login)", updated.Title)
	deps, _ := tr.Dependencies(bob, "WEB-3")
	assert.Len(t, deps, 2, "login blocks logout and the profile page")

	_, err = cs.Apply(bob, proposed.ID, []int{0})
	assert.ErrorIs(t, err, app.ErrConflict, "a changeset is applied once")
	_, err = cs.Reject(bob, proposed.ID)
	assert.ErrorIs(t, err, app.ErrConflict)
}

func TestChangesets_PartialApprovalAppliesOnlyApprovedOperations(t *testing.T) {
	cs, tr := newChangesets(t)
	bob := user(t, "bob", "acme-devs")
	profile := mk(t, tr, tracker.KindTicket, "Profile page")
	proposed, err := cs.Propose(bob, authPlan(profile.Key))
	require.NoError(t, err)

	_, err = cs.Apply(bob, proposed.ID, []int{1})
	assert.ErrorIs(t, err, app.ErrInvalid)
	assert.ErrorContains(t, err, "operation 2 needs operation 1")

	applied, err := cs.Apply(bob, proposed.ID, []int{0, 1, 4})
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1, 4}, applied.Approved)
	assert.Empty(t, applied.Results[2].Key, "not approved, not created")
	items, _ := tr.ListItems(bob, "WEB", "", "", "", "")
	assert.Len(t, items, 3, "profile + epic + login")
	deps, _ := tr.Dependencies(bob, profile.Key)
	assert.Empty(t, deps)
}

func TestChangesets_ValidationAgainstTheProject(t *testing.T) {
	cs, tr := newChangesets(t)
	bob := user(t, "bob", "acme-devs")
	a := mk(t, tr, tracker.KindTicket, "a")
	b := mk(t, tr, tracker.KindTicket, "b")
	_, err := tr.AddDependency(bob, a.Key, app.DirBlocks, b.Key)
	require.NoError(t, err)
	plan := func(ops ...changeset.Op) app.ProposeInput {
		return app.ProposeInput{ProjectKey: "WEB", Title: "t", Ops: ops}
	}

	cases := map[string]app.ProposeInput{
		"cycle with existing edges": plan(blocks(b.Key, a.Key)),
		"cycle inside the changeset": plan(createOp("x", tracker.KindTicket, "x"), blocks("$x", a.Key), blocks(b.Key, "$x"),
			blocks(a.Key, b.Key)),
		"unknown item":         plan(blocks(a.Key, "WEB-99")),
		"existing dependency":  plan(blocks(a.Key, b.Key)),
		"epic is not an epic":  plan(changeset.Op{Kind: changeset.OpCreateItem, Ref: "t", Create: &changeset.CreateItem{Kind: tracker.KindTicket, Title: "t", Epic: a.Key}}),
		"malformed":            plan(),
		"update twice":         plan(changeset.Op{Kind: changeset.OpUpdateItem, Update: &changeset.UpdateItem{Item: a.Key, Title: ptr("1")}}, changeset.Op{Kind: changeset.OpUpdateItem, Update: &changeset.UpdateItem{Item: a.Key, Title: ptr("2")}}),
		"other project's item": plan(blocks(a.Key, "APP-1")),
	}
	_, err = tr.CreateItem(user(t, "alice", "ballet-admins"), app.CreateItemInput{ProjectKey: "APP", Kind: tracker.KindTicket, Title: "x"})
	require.NoError(t, err)
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := cs.Propose(bob, in)
			assert.ErrorIs(t, err, app.ErrInvalid)
		})
	}
}

func TestChangesets_StaleProjectIsRevalidatedAtApply(t *testing.T) {
	cs, tr := newChangesets(t)
	bob := user(t, "bob", "acme-devs")
	a := mk(t, tr, tracker.KindTicket, "a")
	b := mk(t, tr, tracker.KindTicket, "b")
	proposed, err := cs.Propose(bob, app.ProposeInput{ProjectKey: "WEB", Title: "t", Ops: []changeset.Op{
		createOp("c", tracker.KindTicket, "c"), blocks(a.Key, b.Key),
	}})
	require.NoError(t, err)

	// Meanwhile someone adds b → a and another ticket (shifting keys).
	_, err = tr.AddDependency(bob, b.Key, app.DirBlocks, a.Key)
	require.NoError(t, err)
	mk(t, tr, tracker.KindTicket, "d")

	_, err = cs.Apply(bob, proposed.ID, []int{0, 1})
	assert.ErrorIs(t, err, app.ErrInvalid, "a → b now closes a cycle")
	applied, err := cs.Apply(bob, proposed.ID, []int{0})
	require.NoError(t, err)
	assert.Equal(t, "WEB-4", applied.Results[0].Key, "keys are assigned at apply time")
}

func TestChangesets_AuthorizationAndRejection(t *testing.T) {
	cs, tr := newChangesets(t)
	bob := user(t, "bob", "acme-devs")
	carol := user(t, "carol", "acme-viewers")
	in := app.ProposeInput{ProjectKey: "WEB", Title: "t", Ops: []changeset.Op{createOp("x", tracker.KindTicket, "x")}}

	_, err := cs.Propose(carol, in)
	assert.ErrorIs(t, err, app.ErrForbidden, "viewers cannot propose")
	proposed, err := cs.Propose(bob, in)
	require.NoError(t, err)

	got, err := cs.Get(carol, proposed.ID)
	require.NoError(t, err, "viewers can read")
	assert.Equal(t, "t", got.Title)
	list, err := cs.List(carol, "WEB", changeset.StatusProposed)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	_, err = cs.Apply(carol, proposed.ID, []int{0})
	assert.ErrorIs(t, err, app.ErrForbidden)

	planner := auth.WithIdentity(context.Background(), auth.Identity{Kind: auth.KindService, Subject: "planner"})
	allow := &app.Changesets{Store: cs.Store, Tracker: &app.Tracker{
		Items: tr.Items, Deps: tr.Deps, Tenancy: tr.Tenancy, Events: tr.Events, Authz: fakeAuthz{app.ActTrackerWrite: {"*"}, app.ActTrackerRead: {"*"}}, Now: tr.Now, NewID: tr.NewID,
	}}
	_, err = allow.Apply(planner, proposed.ID, []int{0})
	assert.ErrorIs(t, err, app.ErrForbidden, "only humans approve")

	rejected, err := cs.Reject(bob, proposed.ID)
	require.NoError(t, err)
	assert.Equal(t, changeset.StatusRejected, rejected.Status)
	items, _ := tr.ListItems(bob, "WEB", "", "", "", "")
	assert.Empty(t, items)
	_, err = cs.Apply(bob, proposed.ID, []int{0})
	assert.ErrorIs(t, err, app.ErrConflict)
}

func TestChangesets_StoreRejectsAStaleApplicationAtomically(t *testing.T) {
	cs, tr := newChangesets(t)
	bob := user(t, "bob", "acme-devs")
	proposed, err := cs.Propose(bob, app.ProposeInput{ProjectKey: "WEB", Title: "t", Ops: []changeset.Op{
		createOp("x", tracker.KindTicket, "x"),
	}})
	require.NoError(t, err)
	project, err := tr.Tenancy.ProjectByKey(t.Context(), "WEB")
	require.NoError(t, err)
	now := time.Now()
	it := tracker.Item{ID: "new", ProjectID: project.ID, Kind: tracker.KindTicket, Title: "x", State: tracker.StateBacklog,
		Type: tracker.TypeFeature, Policy: tracker.DefaultPolicy, CreatedAt: now, UpdatedAt: now, Version: 1}
	decided := proposed.Changeset
	decided.Status, decided.Approved, decided.DecidedAt, decided.Version = changeset.StatusApplied, []int{0}, now, 2

	// The application assumed item number 7, but the project is at 1.
	err = cs.Store.DecideChangeset(t.Context(), decided, 1, app.ChangesetApplication{
		ProjectID: project.ID, NextItemNumber: 7, Creates: []app.ItemWrite{{Item: it}},
	}, event.Event{Type: "changeset.applied", EntityType: "changeset", EntityID: decided.ID})
	assert.ErrorIs(t, err, app.ErrConflict)

	got, err := cs.Get(bob, proposed.ID)
	require.NoError(t, err)
	assert.Equal(t, changeset.StatusProposed, got.Status, "nothing was written")
	items, _ := tr.ListItems(bob, "WEB", "", "", "", "")
	assert.Empty(t, items)
}

func TestChangesets_ThePlannerProposesButNeverDecides(t *testing.T) {
	cs, _ := newChangesets(t)
	bob := user(t, "bob", "acme-devs")
	asPlanner := app.ActingAsPlanner(bob, "s1")
	proposed, err := cs.Propose(asPlanner, app.ProposeInput{ProjectKey: "WEB", Title: "t", Ops: []changeset.Op{
		createOp("x", tracker.KindTicket, "x"),
	}})
	require.NoError(t, err)
	assert.Equal(t, "planner:s1", proposed.ProposedBy.Subject)
	assert.Equal(t, "bob", proposed.ProposedBy.ActingFor)

	_, err = cs.Apply(asPlanner, proposed.ID, []int{0})
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = cs.Reject(asPlanner, proposed.ID)
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = cs.Apply(bob, proposed.ID, []int{0})
	assert.NoError(t, err, "the human approves")
}
