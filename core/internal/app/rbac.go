package app

import (
	"context"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/kit/auth"
)

// RoleBindingStore persists role bindings.
type RoleBindingStore interface {
	ListRoleBindings(ctx context.Context) ([]rbac.Binding, error)
	RoleBinding(ctx context.Context, id string) (rbac.Binding, error)
	CreateRoleBinding(ctx context.Context, b rbac.Binding, e event.Event) error
	DeleteRoleBinding(ctx context.Context, id string, e event.Event) error
}

// RBAC authorizes humans through role bindings: stored bindings plus
// bootstrap bindings from configuration. Workload identities are never
// authorized through bindings (they use run token capabilities).
type RBAC struct {
	Store     RoleBindingStore
	Bootstrap []rbac.Binding
}

// Authorize implements Authorizer.
func (r *RBAC) Authorize(ctx context.Context, id auth.Identity, action Action, scope Scope) error {
	if id.Kind != auth.KindHuman {
		return ErrForbidden
	}
	bindings, err := r.all(ctx)
	if err != nil {
		return err
	}
	if !rbac.Allowed(bindings, id.Claims, action, scope) {
		return fmt.Errorf("%w: %s", ErrForbidden, action)
	}
	return nil
}

// Effective returns the bindings that apply to id.
func (r *RBAC) Effective(ctx context.Context, id auth.Identity) ([]rbac.Binding, error) {
	if id.Kind != auth.KindHuman {
		return nil, nil
	}
	bindings, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	return rbac.Matching(bindings, id.Claims), nil
}

func (r *RBAC) all(ctx context.Context) ([]rbac.Binding, error) {
	stored, err := r.Store.ListRoleBindings(ctx)
	if err != nil {
		return nil, err
	}
	return append(append([]rbac.Binding{}, r.Bootstrap...), stored...), nil
}

// RoleBindings implements role binding administration.
type RoleBindings struct {
	RBAC    *RBAC
	Tenancy TenancyStore
	Now     func() time.Time
	NewID   func() string
}

// target is the authorization target governing a binding's scope.
func target(s rbac.BindingScope) Scope {
	return Scope{Organization: s.Organization}
}

// List returns the bindings the caller may read, bootstrap bindings first.
func (rb *RoleBindings) List(ctx context.Context) ([]rbac.Binding, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	all, err := rb.RBAC.all(ctx)
	if err != nil {
		return nil, err
	}
	visible := []rbac.Binding{}
	for _, b := range all {
		if rb.RBAC.Authorize(ctx, id, ActRoleBindingRead, target(b.Scope)) == nil {
			visible = append(visible, b)
		}
	}
	return visible, nil
}

// CreateRoleBindingInput is the input of Create.
type CreateRoleBindingInput struct {
	Claim string
	Value string
	Role  string
	Scope string // "platform", "organization:<key>" or "project:<key>"
}

// Create adds a binding. The caller needs role_binding.manage at the
// binding's scope; platform-scope bindings therefore need a platform-admin.
func (rb *RoleBindings) Create(ctx context.Context, in CreateRoleBindingInput) (rbac.Binding, error) {
	id, err := caller(ctx)
	if err != nil {
		return rbac.Binding{}, err
	}
	scope, err := rbac.ParseScope(in.Scope)
	if err != nil {
		return rbac.Binding{}, invalid(err)
	}
	organizationID, err := rb.resolve(ctx, &scope)
	if err != nil {
		return rbac.Binding{}, err
	}
	if err := rb.RBAC.Authorize(ctx, id, ActRoleBindingManage, target(scope)); err != nil {
		return rbac.Binding{}, err
	}
	b := rbac.Binding{
		ID: rb.NewID(), Claim: in.Claim, Value: in.Value, Role: rbac.Role(in.Role), Scope: scope, CreatedAt: rb.Now(),
	}
	if err := b.Validate(); err != nil {
		return rbac.Binding{}, invalid(err)
	}
	e := event.Event{
		Organization: organizationID, EntityType: "role_binding", EntityID: b.ID, Type: "role_binding.created",
		Actor: actorOf(id), OccurredAt: b.CreatedAt,
		Payload: mustJSON(map[string]any{"claim": b.Claim, "value": b.Value, "role": b.Role, "scope": b.Scope.String()}),
	}
	if err := rb.RBAC.Store.CreateRoleBinding(ctx, b, e); err != nil {
		return rbac.Binding{}, err
	}
	return b, nil
}

// Delete removes a stored binding. Bootstrap bindings cannot be deleted.
func (rb *RoleBindings) Delete(ctx context.Context, bindingID string) error {
	id, err := caller(ctx)
	if err != nil {
		return err
	}
	b, err := rb.RBAC.Store.RoleBinding(ctx, bindingID)
	if err != nil {
		return err
	}
	if err := rb.RBAC.Authorize(ctx, id, ActRoleBindingManage, target(b.Scope)); err != nil {
		return err
	}
	scope := b.Scope
	organizationID, err := rb.resolve(ctx, &scope)
	if err != nil {
		return err
	}
	e := event.Event{
		Organization: organizationID, EntityType: "role_binding", EntityID: b.ID, Type: "role_binding.deleted",
		Actor: actorOf(id), OccurredAt: rb.Now(),
		Payload: mustJSON(map[string]any{"claim": b.Claim, "value": b.Value, "role": b.Role, "scope": b.Scope.String()}),
	}
	return rb.RBAC.Store.DeleteRoleBinding(ctx, b.ID, e)
}

// resolve checks that the scope's organization/project exist, fills in the
// organization of a project scope, and returns the organization's ID ("" for
// platform scope).
func (rb *RoleBindings) resolve(ctx context.Context, s *rbac.BindingScope) (string, error) {
	switch s.Kind {
	case rbac.ScopeOrganization:
		c, err := rb.Tenancy.OrganizationByKey(ctx, s.Organization)
		if err != nil {
			return "", err
		}
		return c.ID, nil
	case rbac.ScopeProject:
		p, err := rb.Tenancy.ProjectByKey(ctx, s.Project)
		if err != nil {
			return "", err
		}
		c, err := rb.Tenancy.OrganizationByID(ctx, p.OrganizationID)
		if err != nil {
			return "", err
		}
		s.Organization = c.Key
		return c.ID, nil
	}
	return "", nil
}
