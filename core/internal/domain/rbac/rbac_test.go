package rbac_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
)

var groups = func(g ...string) map[string]any {
	items := make([]any, len(g))
	for i, s := range g {
		items[i] = s
	}
	return map[string]any{"groups": items}
}

func bind(role rbac.Role, scope rbac.BindingScope) rbac.Binding {
	return rbac.Binding{Claim: "groups", Value: "team", Role: role, Scope: scope}
}

var (
	org     = rbac.BindingScope{Kind: rbac.ScopeOrganization}
	acme    = rbac.BindingScope{Kind: rbac.ScopeCustomer, Customer: "acme"}
	acmeWeb = rbac.BindingScope{Kind: rbac.ScopeProject, Customer: "acme", Project: "WEB"}
)

// expected[role] lists the actions each role grants; everything else is denied.
var expected = map[rbac.Role][]rbac.Action{
	rbac.RoleOrgAdmin: rbac.AllActions,
	rbac.RoleCustomerAdmin: {
		rbac.ActCustomerRead, rbac.ActCustomerUpdate, rbac.ActProjectCreate, rbac.ActProjectRead,
		rbac.ActProjectUpdate, rbac.ActRoleBindingManage, rbac.ActRoleBindingRead,
	},
	rbac.RoleEngineer: {rbac.ActCustomerRead, rbac.ActProjectRead},
	rbac.RoleApprover: {rbac.ActCustomerRead, rbac.ActProjectRead},
	rbac.RoleViewer:   {rbac.ActCustomerRead, rbac.ActProjectRead},
}

func TestRoles_GrantExactlyTheirActions(t *testing.T) {
	for _, role := range rbac.AllRoles {
		for _, action := range rbac.AllActions {
			want := false
			for _, a := range expected[role] {
				want = want || a == action
			}
			t.Run(fmt.Sprintf("%s/%s", role, action), func(t *testing.T) {
				assert.Equal(t, want, role.Allows(action))
			})
		}
	}
}

func TestAllowed_ScopeCoverage(t *testing.T) {
	tests := []struct {
		name    string
		binding rbac.Binding
		action  rbac.Action
		target  rbac.Target
		want    bool
	}{
		{"org binding covers org-level action", bind(rbac.RoleOrgAdmin, org), rbac.ActCustomerCreate, rbac.Target{}, true},
		{"org binding covers any project", bind(rbac.RoleOrgAdmin, org), rbac.ActProjectUpdate, rbac.Target{Customer: "x", Project: "Y"}, true},
		{"customer binding covers its customer", bind(rbac.RoleViewer, acme), rbac.ActCustomerRead, rbac.Target{Customer: "acme"}, true},
		{"customer binding covers its projects", bind(rbac.RoleViewer, acme), rbac.ActProjectRead, rbac.Target{Customer: "acme", Project: "WEB"}, true},
		{"customer binding does not cover other customer", bind(rbac.RoleViewer, acme), rbac.ActCustomerRead, rbac.Target{Customer: "globex"}, false},
		{"customer binding does not cover org level", bind(rbac.RoleCustomerAdmin, acme), rbac.ActCustomerCreate, rbac.Target{}, false},
		{"project binding covers its project", bind(rbac.RoleViewer, acmeWeb), rbac.ActProjectRead, rbac.Target{Customer: "acme", Project: "WEB"}, true},
		{"project binding sees parent customer", bind(rbac.RoleViewer, acmeWeb), rbac.ActCustomerRead, rbac.Target{Customer: "acme"}, true},
		{"project binding does not cover sibling project", bind(rbac.RoleViewer, acmeWeb), rbac.ActProjectRead, rbac.Target{Customer: "acme", Project: "APP"}, false},
		{"project binding cannot update parent customer", bind(rbac.RoleEngineer, acmeWeb), rbac.ActCustomerUpdate, rbac.Target{Customer: "acme"}, false},
		{"role must grant action", bind(rbac.RoleViewer, acme), rbac.ActProjectUpdate, rbac.Target{Customer: "acme", Project: "WEB"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rbac.Allowed([]rbac.Binding{tt.binding}, groups("team"), tt.action, tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAllowed_RequiresMatchingClaim(t *testing.T) {
	b := bind(rbac.RoleOrgAdmin, org)

	assert.False(t, rbac.Allowed([]rbac.Binding{b}, groups("other"), rbac.ActCustomerRead, rbac.Target{Customer: "acme"}))
	assert.False(t, rbac.Allowed([]rbac.Binding{b}, map[string]any{}, rbac.ActCustomerRead, rbac.Target{Customer: "acme"}))
	assert.False(t, rbac.Allowed(nil, groups("team"), rbac.ActCustomerRead, rbac.Target{Customer: "acme"}), "deny by default")
}

func TestBinding_Matches(t *testing.T) {
	email := rbac.Binding{Claim: "email", Value: "pm@acme.test"}
	assert.True(t, email.Matches(map[string]any{"email": "pm@acme.test"}))
	assert.False(t, email.Matches(map[string]any{"email": "other@acme.test"}))
	assert.True(t, bind(rbac.RoleViewer, acme).Matches(map[string]any{"groups": []string{"team"}}))
	assert.False(t, email.Matches(map[string]any{"email": 42}))
}

func TestBinding_Validate(t *testing.T) {
	assert.NoError(t, bind(rbac.RoleOrgAdmin, org).Validate())
	assert.NoError(t, bind(rbac.RoleEngineer, acmeWeb).Validate())
	assert.Error(t, bind(rbac.RoleOrgAdmin, acme).Validate(), "org-admin only at organization scope")
	assert.Error(t, bind(rbac.RoleCustomerAdmin, acmeWeb).Validate())
	assert.Error(t, bind("superuser", org).Validate())
	assert.Error(t, rbac.Binding{Claim: "", Value: "x", Role: rbac.RoleViewer, Scope: org}.Validate())
}

func TestParseScope(t *testing.T) {
	for in, want := range map[string]rbac.BindingScope{
		"organization":  org,
		"customer:acme": {Kind: rbac.ScopeCustomer, Customer: "acme"},
		"project:WEB":   {Kind: rbac.ScopeProject, Project: "WEB"},
	} {
		got, err := rbac.ParseScope(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got)
		assert.Equal(t, in, got.String())
	}
	for _, bad := range []string{"", "org", "customer:", "team:x"} {
		_, err := rbac.ParseScope(bad)
		assert.Error(t, err, bad)
	}
}

func TestParseBootstrap(t *testing.T) {
	b, err := rbac.ParseBootstrap("groups:ballet-admins")
	require.NoError(t, err)
	assert.Equal(t, rbac.RoleOrgAdmin, b.Role)
	assert.True(t, b.Bootstrap)
	assert.True(t, b.Matches(groups("ballet-admins")))

	_, err = rbac.ParseBootstrap("ballet-admins")
	assert.Error(t, err)
}
