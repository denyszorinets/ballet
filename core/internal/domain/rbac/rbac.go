// Package rbac is Ballet's role-based access policy (ADR-0006): role
// bindings map identity claims to roles at a scope, and roles grant
// actions. Evaluation is deny-by-default and pure.
package rbac

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Action is an operation subject to authorization.
type Action string

// Actions. Read actions are listed in readActions.
const (
	ActOrganizationCreate Action = "organization.create"
	ActOrganizationRead   Action = "organization.read"
	ActOrganizationUpdate Action = "organization.update"
	ActProjectCreate      Action = "project.create"
	ActProjectRead        Action = "project.read"
	ActProjectUpdate      Action = "project.update"
	ActRoleBindingManage  Action = "role_binding.manage"
	ActRoleBindingRead    Action = "role_binding.read"
	ActTrackerRead        Action = "tracker.read"      // milestones, epics, tickets, dependencies, history
	ActTrackerWrite       Action = "tracker.write"     // create, edit and transition them
	ActCredentialManage   Action = "credential.manage" // LLM provider credentials (never readable)
	ActKnowledgeRead      Action = "knowledge.read"    // the organization's knowledge space
	ActKnowledgeWrite     Action = "knowledge.write"
	ActSkillRead          Action = "skill.read"  // agent skills (process)
	ActSkillWrite         Action = "skill.write" // edit and publish skills
	ActRunManage          Action = "run.manage"  // queue and cancel agent runs by hand
)

// AllActions lists every action, for documentation and exhaustive tests.
var AllActions = []Action{
	ActOrganizationCreate, ActOrganizationRead, ActOrganizationUpdate,
	ActProjectCreate, ActProjectRead, ActProjectUpdate,
	ActRoleBindingManage, ActRoleBindingRead,
	ActTrackerRead, ActTrackerWrite,
	ActCredentialManage, ActKnowledgeRead, ActKnowledgeWrite, ActSkillRead, ActSkillWrite, ActRunManage,
}

// Role is a named set of actions.
type Role string

// Roles.
const (
	RolePlatformAdmin     Role = "platform-admin"
	RoleOrganizationAdmin Role = "organization-admin"
	RoleEngineer          Role = "engineer"
	RoleApprover          Role = "approver"
	RoleViewer            Role = "viewer"
)

// AllRoles lists every role.
var AllRoles = []Role{RolePlatformAdmin, RoleOrganizationAdmin, RoleEngineer, RoleApprover, RoleViewer}

var readActions = []Action{ActOrganizationRead, ActProjectRead, ActTrackerRead, ActKnowledgeRead, ActSkillRead}

var grants = map[Role][]Action{
	RolePlatformAdmin: AllActions,
	RoleOrganizationAdmin: {
		ActOrganizationRead, ActOrganizationUpdate, ActProjectCreate, ActProjectRead, ActProjectUpdate,
		ActRoleBindingManage, ActRoleBindingRead, ActTrackerRead, ActTrackerWrite, ActCredentialManage,
		ActKnowledgeRead, ActKnowledgeWrite, ActSkillRead, ActSkillWrite, ActRunManage,
	},
	RoleEngineer: append(slices.Clone(readActions), ActTrackerWrite, ActKnowledgeWrite),
	RoleApprover: readActions,
	RoleViewer:   readActions,
}

// Actions returns the actions a role grants.
func (r Role) Actions() []Action { return grants[r] }

// Allows reports whether the role grants the action.
func (r Role) Allows(a Action) bool { return slices.Contains(grants[r], a) }

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return slices.Contains(AllRoles, r) }

// ScopeKind is the level a binding applies at.
type ScopeKind string

// Scope kinds.
const (
	ScopePlatform     ScopeKind = "platform"
	ScopeOrganization ScopeKind = "organization"
	ScopeProject      ScopeKind = "project"
)

// BindingScope is where a binding applies. Organization is set for organization and
// project scopes (a project binding records its project's organization).
type BindingScope struct {
	Kind         ScopeKind
	Organization string
	Project      string
}

// String formats the scope as "platform", "organization:<key>" or
// "project:<key>".
func (s BindingScope) String() string {
	switch s.Kind {
	case ScopeOrganization:
		return "organization:" + s.Organization
	case ScopeProject:
		return "project:" + s.Project
	default:
		return string(ScopePlatform)
	}
}

// ParseScope parses "platform", "organization:<key>" or "project:<key>".
// The organization of a project scope must be resolved by the caller.
func ParseScope(s string) (BindingScope, error) {
	if s == string(ScopePlatform) {
		return BindingScope{Kind: ScopePlatform}, nil
	}
	kind, key, ok := strings.Cut(s, ":")
	if !ok || key == "" {
		return BindingScope{}, fmt.Errorf("scope %q must be platform, organization:<key> or project:<key>", s)
	}
	switch ScopeKind(kind) {
	case ScopeOrganization:
		return BindingScope{Kind: ScopeOrganization, Organization: key}, nil
	case ScopeProject:
		return BindingScope{Kind: ScopeProject, Project: key}, nil
	}
	return BindingScope{}, fmt.Errorf("scope %q must be platform, organization:<key> or project:<key>", s)
}

// Target is the scope of a requested action. Empty fields mean platform
// level; a project target carries its organization.
type Target struct {
	Organization string
	Project      string
}

// covers reports whether a binding at s applies to an action on t.
func (s BindingScope) covers(t Target, a Action) bool {
	switch s.Kind {
	case ScopePlatform:
		return true
	case ScopeOrganization:
		return t.Organization == s.Organization
	case ScopeProject:
		if t.Project == s.Project {
			return true
		}
		// Seeing a project implies seeing its organization.
		return a == ActOrganizationRead && t.Project == "" && t.Organization == s.Organization
	}
	return false
}

// Binding grants Role at Scope to identities whose Claim matches Value.
type Binding struct {
	ID        string
	Claim     string // e.g. "groups", "email", "sub"
	Value     string
	Role      Role
	Scope     BindingScope
	Bootstrap bool // from configuration; not stored, cannot be deleted
	CreatedAt time.Time
}

// Validate checks the binding's fields (not whether its scope exists).
func (b Binding) Validate() error {
	var errs []error
	if strings.TrimSpace(b.Claim) == "" || strings.TrimSpace(b.Value) == "" {
		errs = append(errs, errors.New("claim and value must not be empty"))
	}
	if !b.Role.Valid() {
		errs = append(errs, fmt.Errorf("unknown role %q", b.Role))
	}
	if b.Role == RolePlatformAdmin && b.Scope.Kind != ScopePlatform {
		errs = append(errs, errors.New("platform-admin can only be bound at platform scope"))
	}
	if b.Role == RoleOrganizationAdmin && b.Scope.Kind == ScopeProject {
		errs = append(errs, errors.New("organization-admin cannot be bound at project scope"))
	}
	return errors.Join(errs...)
}

// Matches reports whether claims satisfy the binding: a string claim must
// equal Value; a list claim must contain it.
func (b Binding) Matches(claims map[string]any) bool {
	switch v := claims[b.Claim].(type) {
	case string:
		return v == b.Value
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == b.Value {
				return true
			}
		}
	case []string:
		return slices.Contains(v, b.Value)
	}
	return false
}

// Allowed reports whether any binding matching claims grants action a on t.
func Allowed(bindings []Binding, claims map[string]any, a Action, t Target) bool {
	for _, b := range bindings {
		if b.Role.Allows(a) && b.Scope.covers(t, a) && b.Matches(claims) {
			return true
		}
	}
	return false
}

// Matching returns the bindings whose claim matches.
func Matching(bindings []Binding, claims map[string]any) []Binding {
	var out []Binding
	for _, b := range bindings {
		if b.Matches(claims) {
			out = append(out, b)
		}
	}
	return out
}

// ParseBootstrap parses "claim:value" into a platform-admin binding.
func ParseBootstrap(s string) (Binding, error) {
	claim, value, ok := strings.Cut(s, ":")
	if !ok || claim == "" || value == "" {
		return Binding{}, fmt.Errorf("bootstrap admin %q must be claim:value, e.g. groups:ballet-admins", s)
	}
	return Binding{
		ID: "bootstrap:" + s, Claim: claim, Value: value, Role: RolePlatformAdmin,
		Scope: BindingScope{Kind: ScopePlatform}, Bootstrap: true,
	}, nil
}
