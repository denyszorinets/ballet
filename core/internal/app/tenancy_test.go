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

// fakeAuthz allows exactly the (action, customer) pairs it was given; an
// empty customer means platform level.
type fakeAuthz map[app.Action][]string

func (f fakeAuthz) Authorize(_ context.Context, _ auth.Identity, a app.Action, s app.Scope) error {
	for _, c := range f[a] {
		if c == "*" || c == s.Customer {
			return nil
		}
	}
	return app.ErrForbidden
}

var allowAll = fakeAuthz{
	app.ActCustomerCreate: {"*"}, app.ActCustomerRead: {"*"}, app.ActCustomerUpdate: {"*"},
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

func TestTenancy_CreateAndReadCustomerAndProject(t *testing.T) {
	ten, st := newTenancy(t, allowAll)
	ctx := asUser(t, "alice")

	c, err := ten.CreateCustomer(ctx, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), c.Version)

	p, err := ten.CreateProject(ctx, app.CreateProjectInput{CustomerKey: "acme", Key: "ACME", Name: "Acme Shop"})
	require.NoError(t, err)
	assert.Equal(t, c.ID, p.CustomerID)

	got, err := ten.GetProject(ctx, "ACME")
	require.NoError(t, err)
	assert.Equal(t, "acme", got.CustomerKey)
	assert.Equal(t, "Acme Shop", got.Name)

	events, err := st.ListEvents(t.Context(), store.EventFilter{Customer: c.ID})
	require.NoError(t, err)
	require.Len(t, events, 2, "one event per mutation")
	assert.Equal(t, "customer.created", events[0].Type)
	assert.Equal(t, "project.created", events[1].Type)
	assert.Equal(t, "alice", events[1].Actor.Subject)
}

func TestTenancy_Errors(t *testing.T) {
	ten, _ := newTenancy(t, allowAll)
	ctx := asUser(t, "alice")
	_, err := ten.CreateCustomer(ctx, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)

	_, err = ten.CreateCustomer(ctx, app.CreateCustomerInput{Key: "acme", Name: "Again"})
	assert.ErrorIs(t, err, app.ErrAlreadyExists)

	_, err = ten.CreateCustomer(ctx, app.CreateCustomerInput{Key: "Bad Key", Name: "X"})
	assert.ErrorIs(t, err, app.ErrInvalid)

	_, err = ten.CreateProject(ctx, app.CreateProjectInput{CustomerKey: "nobody", Key: "NOPE", Name: "X"})
	assert.ErrorIs(t, err, app.ErrNotFound)

	_, err = ten.GetCustomer(ctx, "missing")
	assert.ErrorIs(t, err, app.ErrNotFound)

	_, err = ten.ListCustomers(t.Context())
	assert.ErrorIs(t, err, app.ErrUnauthorized, "no identity in context")
}

func TestTenancy_UpdateUsesOptimisticConcurrency(t *testing.T) {
	ten, _ := newTenancy(t, allowAll)
	ctx := asUser(t, "alice")
	c, err := ten.CreateCustomer(ctx, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)

	updated, err := ten.UpdateCustomer(ctx, app.UpdateCustomerInput{Key: "acme", Name: "Acme Corp", Version: c.Version})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)

	_, err = ten.UpdateCustomer(ctx, app.UpdateCustomerInput{Key: "acme", Name: "Stale", Version: c.Version})
	assert.ErrorIs(t, err, app.ErrConflict)

	got, err := ten.GetCustomer(ctx, "acme")
	require.NoError(t, err)
	assert.Equal(t, "Acme Corp", got.Name)
}

func TestTenancy_AuthorizationScopesVisibility(t *testing.T) {
	admin, _ := newTenancy(t, allowAll)
	ctx := asUser(t, "alice")
	for customer, project := range map[string]string{"acme": "PAX", "globex": "PGX"} {
		_, err := admin.CreateCustomer(ctx, app.CreateCustomerInput{Key: customer, Name: customer})
		require.NoError(t, err)
		_, err = admin.CreateProject(ctx, app.CreateProjectInput{CustomerKey: customer, Key: project, Name: customer})
		require.NoError(t, err)
	}

	// Same database, an authorizer that only allows reading customer "acme".
	scoped := &app.Tenancy{Store: admin.Store, Authz: fakeAuthz{
		app.ActCustomerRead: {"acme"}, app.ActProjectRead: {"acme"},
	}, Now: time.Now, NewID: store.NewID}
	bob := asUser(t, "bob")

	customers, err := scoped.ListCustomers(bob)
	require.NoError(t, err)
	require.Len(t, customers, 1)
	assert.Equal(t, "acme", customers[0].Key)

	_, err = scoped.GetCustomer(bob, "globex")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = scoped.GetProject(bob, "PGX")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = scoped.CreateCustomer(bob, app.CreateCustomerInput{Key: "initech", Name: "Initech"})
	assert.ErrorIs(t, err, app.ErrForbidden)

	projects, err := scoped.ListProjects(bob, "acme")
	require.NoError(t, err)
	assert.Len(t, projects, 1)
}

func TestTenancy_DenyAllDeniesEverything(t *testing.T) {
	ten, _ := newTenancy(t, app.DenyAll{})

	_, err := ten.CreateCustomer(asUser(t, "alice"), app.CreateCustomerInput{Key: "acme", Name: "Acme"})

	assert.ErrorIs(t, err, app.ErrForbidden)
}
