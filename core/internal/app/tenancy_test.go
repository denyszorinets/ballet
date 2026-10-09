package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/kit/auth"
)

// fakeAuthz allows exactly the (action, organization) pairs it was given; an
// empty organization means platform level.
type fakeAuthz map[app.Action][]string

func (f fakeAuthz) Authorize(_ context.Context, _ auth.Identity, a app.Action, s app.Scope) error {
	for _, c := range f[a] {
		if c == "*" || c == s.Organization {
			return nil
		}
	}
	return app.ErrForbidden
}

var allowAll = fakeAuthz{
	app.ActOrganizationCreate: {"*"}, app.ActOrganizationRead: {"*"}, app.ActOrganizationUpdate: {"*"},
	app.ActProjectCreate: {"*"}, app.ActProjectRead: {"*"}, app.ActProjectUpdate: {"*"},
}

func newTenancy(t *testing.T, authz app.Authorizer) (*app.Tenancy, *store.Store) {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir()+"/core.db")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID}, st
}

func asUser(t *testing.T, subject string) context.Context {
	return auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: subject})
}

func TestTenancy_CreateAndReadOrganizationAndProject(t *testing.T) {
	ten, st := newTenancy(t, allowAll)
	ctx := asUser(t, "alice")

	c, err := ten.CreateOrganization(ctx, app.CreateOrganizationInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), c.Version)

	p, err := ten.CreateProject(ctx, app.CreateProjectInput{OrganizationKey: "acme", Key: "ACME", Name: "Acme Shop"})
	require.NoError(t, err)
	assert.Equal(t, c.ID, p.OrganizationID)

	got, err := ten.GetProject(ctx, "ACME")
	require.NoError(t, err)
	assert.Equal(t, "acme", got.OrganizationKey)
	assert.Equal(t, "Acme Shop", got.Name)

	events, err := st.ListEvents(t.Context(), store.EventFilter{Organization: c.ID})
	require.NoError(t, err)
	require.Len(t, events, 2, "one event per mutation")
	assert.Equal(t, "organization.created", events[0].Type)
	assert.Equal(t, "project.created", events[1].Type)
	assert.Equal(t, "alice", events[1].Actor.Subject)
}

func TestTenancy_Errors(t *testing.T) {
	ten, _ := newTenancy(t, allowAll)
	ctx := asUser(t, "alice")
	_, err := ten.CreateOrganization(ctx, app.CreateOrganizationInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)

	_, err = ten.CreateOrganization(ctx, app.CreateOrganizationInput{Key: "acme", Name: "Again"})
	assert.ErrorIs(t, err, app.ErrAlreadyExists)

	_, err = ten.CreateOrganization(ctx, app.CreateOrganizationInput{Key: "Bad Key", Name: "X"})
	assert.ErrorIs(t, err, app.ErrInvalid)

	_, err = ten.CreateProject(ctx, app.CreateProjectInput{OrganizationKey: "nobody", Key: "NOPE", Name: "X"})
	assert.ErrorIs(t, err, app.ErrNotFound)

	_, err = ten.GetOrganization(ctx, "missing")
	assert.ErrorIs(t, err, app.ErrNotFound)

	_, err = ten.ListOrganizations(t.Context())
	assert.ErrorIs(t, err, app.ErrUnauthorized, "no identity in context")
}

func TestTenancy_UpdateUsesOptimisticConcurrency(t *testing.T) {
	ten, _ := newTenancy(t, allowAll)
	ctx := asUser(t, "alice")
	c, err := ten.CreateOrganization(ctx, app.CreateOrganizationInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)

	updated, err := ten.UpdateOrganization(ctx, app.UpdateOrganizationInput{Key: "acme", Name: "Acme Corp", Version: c.Version})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)

	_, err = ten.UpdateOrganization(ctx, app.UpdateOrganizationInput{Key: "acme", Name: "Stale", Version: c.Version})
	assert.ErrorIs(t, err, app.ErrConflict)

	got, err := ten.GetOrganization(ctx, "acme")
	require.NoError(t, err)
	assert.Equal(t, "Acme Corp", got.Name)
}

func TestTenancy_AuthorizationScopesVisibility(t *testing.T) {
	admin, _ := newTenancy(t, allowAll)
	ctx := asUser(t, "alice")
	for organization, project := range map[string]string{"acme": "PAX", "globex": "PGX"} {
		_, err := admin.CreateOrganization(ctx, app.CreateOrganizationInput{Key: organization, Name: organization})
		require.NoError(t, err)
		_, err = admin.CreateProject(ctx, app.CreateProjectInput{OrganizationKey: organization, Key: project, Name: organization})
		require.NoError(t, err)
	}

	// Same database, an authorizer that only allows reading organization "acme".
	scoped := &app.Tenancy{Store: admin.Store, Authz: fakeAuthz{
		app.ActOrganizationRead: {"acme"}, app.ActProjectRead: {"acme"},
	}, Now: time.Now, NewID: store.NewID}
	bob := asUser(t, "bob")

	organizations, err := scoped.ListOrganizations(bob)
	require.NoError(t, err)
	require.Len(t, organizations, 1)
	assert.Equal(t, "acme", organizations[0].Key)

	_, err = scoped.GetOrganization(bob, "globex")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = scoped.GetProject(bob, "PGX")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = scoped.CreateOrganization(bob, app.CreateOrganizationInput{Key: "initech", Name: "Initech"})
	assert.ErrorIs(t, err, app.ErrForbidden)

	projects, err := scoped.ListProjects(bob, "acme")
	require.NoError(t, err)
	assert.Len(t, projects, 1)
}

func TestTenancy_DenyAllDeniesEverything(t *testing.T) {
	ten, _ := newTenancy(t, app.DenyAll{})

	_, err := ten.CreateOrganization(asUser(t, "alice"), app.CreateOrganizationInput{Key: "acme", Name: "Acme"})

	assert.ErrorIs(t, err, app.ErrForbidden)
}
