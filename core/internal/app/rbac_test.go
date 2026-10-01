package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/kit/auth"
)

type rbacEnv struct {
	tenancy  *app.Tenancy
	bindings *app.RoleBindings
	rbac     *app.RBAC
	store    *store.Store
}

func newRBACEnv(t *testing.T) rbacEnv {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir()+"/core.db")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	boot, err := rbac.ParseBootstrap("groups:ballet-admins")
	require.NoError(t, err)
	r := &app.RBAC{Store: st, Bootstrap: []rbac.Binding{boot}}
	return rbacEnv{
		tenancy:  &app.Tenancy{Store: st, Authz: r, Now: time.Now, NewID: store.NewID},
		bindings: &app.RoleBindings{RBAC: r, Tenancy: st, Now: time.Now, NewID: store.NewID},
		rbac:     r,
		store:    st,
	}
}

func user(t *testing.T, subject string, groups ...string) context.Context {
	items := make([]any, len(groups))
	for i, g := range groups {
		items[i] = g
	}
	return auth.WithIdentity(t.Context(), auth.Identity{
		Kind: auth.KindHuman, Subject: subject, Claims: map[string]any{"groups": items, "sub": subject},
	})
}

// seed creates customers acme (project WEB, APP) and globex (project GLX)
// as the bootstrap admin, and binds acme-devs as engineers and acme-viewers
// as viewers of acme.
func seed(t *testing.T, env rbacEnv) {
	t.Helper()
	alice := user(t, "alice", "ballet-admins")
	for _, c := range []string{"acme", "globex"} {
		_, err := env.tenancy.CreateCustomer(alice, app.CreateCustomerInput{Key: c, Name: c})
		require.NoError(t, err)
	}
	for p, c := range map[string]string{"WEB": "acme", "APP": "acme", "GLX": "globex"} {
		_, err := env.tenancy.CreateProject(alice, app.CreateProjectInput{CustomerKey: c, Key: p, Name: p})
		require.NoError(t, err)
	}
	for _, in := range []app.CreateRoleBindingInput{
		{Claim: "groups", Value: "acme-devs", Role: "engineer", Scope: "customer:acme"},
		{Claim: "groups", Value: "acme-viewers", Role: "viewer", Scope: "project:WEB"},
		{Claim: "groups", Value: "acme-admins", Role: "customer-admin", Scope: "customer:acme"},
	} {
		_, err := env.bindings.Create(alice, in)
		require.NoError(t, err, in)
	}
}

func keys(cs []app.ProjectView) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Key)
	}
	return out
}

func TestRBAC_EngineerSeesOnlyOwnCustomer(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	bob := user(t, "bob", "acme-devs")

	customers, err := env.tenancy.ListCustomers(bob)
	require.NoError(t, err)
	require.Len(t, customers, 1)
	assert.Equal(t, "acme", customers[0].Key)

	projects, err := env.tenancy.ListProjects(bob, "acme")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"APP", "WEB"}, keys(projects))

	_, err = env.tenancy.GetProject(bob, "GLX")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = env.tenancy.CreateCustomer(bob, app.CreateCustomerInput{Key: "initech", Name: "I"})
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = env.tenancy.UpdateProject(bob, app.UpdateProjectInput{Key: "WEB", Name: "x", Version: 1})
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers read projects but do not administer them")
}

func TestRBAC_ProjectViewerSeesProjectAndItsCustomerOnly(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	carol := user(t, "carol", "acme-viewers")

	customers, err := env.tenancy.ListCustomers(carol)
	require.NoError(t, err)
	require.Len(t, customers, 1)

	projects, err := env.tenancy.ListProjects(carol, "acme")
	require.NoError(t, err)
	assert.Equal(t, []string{"WEB"}, keys(projects))
}

func TestRBAC_CustomerAdminManagesOnlyWithinCustomer(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	dave := user(t, "dave", "acme-admins")

	_, err := env.tenancy.CreateProject(dave, app.CreateProjectInput{CustomerKey: "acme", Key: "API", Name: "API"})
	require.NoError(t, err)
	_, err = env.tenancy.CreateProject(dave, app.CreateProjectInput{CustomerKey: "globex", Key: "GX2", Name: "x"})
	assert.ErrorIs(t, err, app.ErrForbidden)

	_, err = env.bindings.Create(dave, app.CreateRoleBindingInput{Claim: "email", Value: "pm@acme.test", Role: "approver", Scope: "project:API"})
	require.NoError(t, err)
	_, err = env.bindings.Create(dave, app.CreateRoleBindingInput{Claim: "groups", Value: "acme-admins", Role: "org-admin", Scope: "organization"})
	assert.ErrorIs(t, err, app.ErrForbidden, "cannot escalate to organization scope")
	_, err = env.bindings.Create(dave, app.CreateRoleBindingInput{Claim: "groups", Value: "x", Role: "viewer", Scope: "customer:globex"})
	assert.ErrorIs(t, err, app.ErrForbidden)

	visible, err := env.bindings.List(dave)
	require.NoError(t, err)
	for _, b := range visible {
		assert.Equal(t, "acme", b.Scope.Customer, "customer admins only see their customer's bindings")
	}
}

func TestRoleBindings_CreateValidatesAndResolvesScopes(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	alice := user(t, "alice", "ballet-admins")

	b, err := env.bindings.Create(alice, app.CreateRoleBindingInput{Claim: "groups", Value: "web", Role: "engineer", Scope: "project:WEB"})
	require.NoError(t, err)
	assert.Equal(t, "acme", b.Scope.Customer, "project scope records its customer")

	for name, in := range map[string]app.CreateRoleBindingInput{
		"bad scope":        {Claim: "groups", Value: "x", Role: "viewer", Scope: "team:x"},
		"unknown role":     {Claim: "groups", Value: "x", Role: "root", Scope: "organization"},
		"org-admin scoped": {Claim: "groups", Value: "x", Role: "org-admin", Scope: "customer:acme"},
		"empty claim":      {Claim: "", Value: "x", Role: "viewer", Scope: "organization"},
	} {
		_, err := env.bindings.Create(alice, in)
		assert.ErrorIs(t, err, app.ErrInvalid, name)
	}
	_, err = env.bindings.Create(alice, app.CreateRoleBindingInput{Claim: "groups", Value: "x", Role: "viewer", Scope: "project:NOPE"})
	assert.ErrorIs(t, err, app.ErrNotFound)
	_, err = env.bindings.Create(alice, app.CreateRoleBindingInput{Claim: "groups", Value: "web", Role: "engineer", Scope: "project:WEB"})
	assert.ErrorIs(t, err, app.ErrAlreadyExists)
}

func TestRoleBindings_DeleteRevokesAccess(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	alice := user(t, "alice", "ballet-admins")
	bob := user(t, "bob", "acme-devs")

	all, err := env.bindings.List(alice)
	require.NoError(t, err)
	var devs rbac.Binding
	for _, b := range all {
		if b.Value == "acme-devs" {
			devs = b
		}
	}
	require.NotEmpty(t, devs.ID)

	require.NoError(t, env.bindings.Delete(alice, devs.ID))

	_, err = env.tenancy.GetCustomer(bob, "acme")
	assert.ErrorIs(t, err, app.ErrForbidden)
	assert.ErrorIs(t, env.bindings.Delete(alice, devs.ID), app.ErrNotFound)
	assert.ErrorIs(t, env.bindings.Delete(alice, all[0].ID), app.ErrNotFound, "bootstrap bindings are not stored")

	events, err := env.store.ListEvents(t.Context(), store.EventFilter{EntityType: "role_binding", EntityID: devs.ID})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "role_binding.deleted", events[1].Type)
}

func TestRBAC_WorkloadsAndUnboundUsersAreDenied(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	svc := auth.WithIdentity(t.Context(), auth.Identity{
		Kind: auth.KindService, Subject: "run:1", Claims: map[string]any{"groups": []any{"ballet-admins"}},
	})

	_, err := env.tenancy.GetCustomer(svc, "acme")
	assert.ErrorIs(t, err, app.ErrForbidden)

	customers, err := env.tenancy.ListCustomers(user(t, "eve"))
	require.NoError(t, err)
	assert.Empty(t, customers)
}

func TestRBAC_Effective(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)

	got, err := env.rbac.Effective(t.Context(), auth.Identity{
		Kind: auth.KindHuman, Claims: map[string]any{"groups": []any{"acme-devs", "acme-viewers"}},
	})

	require.NoError(t, err)
	var scopes []string
	for _, b := range got {
		scopes = append(scopes, string(b.Role)+"@"+b.Scope.String())
	}
	assert.ElementsMatch(t, []string{"engineer@customer:acme", "viewer@project:WEB"}, scopes)
}
