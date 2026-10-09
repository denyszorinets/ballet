package app

import (
	"context"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/kit/auth"
)

// Action is an operation subject to authorization (defined by the rbac policy).
type Action = rbac.Action

// Actions used by use cases.
const (
	ActCustomerCreate    = rbac.ActCustomerCreate
	ActCustomerRead      = rbac.ActCustomerRead
	ActCustomerUpdate    = rbac.ActCustomerUpdate
	ActProjectCreate     = rbac.ActProjectCreate
	ActProjectRead       = rbac.ActProjectRead
	ActProjectUpdate     = rbac.ActProjectUpdate
	ActRoleBindingManage = rbac.ActRoleBindingManage
	ActRoleBindingRead   = rbac.ActRoleBindingRead
	ActTrackerRead       = rbac.ActTrackerRead
	ActTrackerWrite      = rbac.ActTrackerWrite
	ActCredentialManage  = rbac.ActCredentialManage
	ActKnowledgeRead     = rbac.ActKnowledgeRead
	ActKnowledgeWrite    = rbac.ActKnowledgeWrite
	ActSkillRead         = rbac.ActSkillRead
	ActSkillWrite        = rbac.ActSkillWrite
	ActRunManage         = rbac.ActRunManage
)

// Scope is where an action applies: customer and project keys; empty
// fields mea platform level.
type Scope = rbac.Target

// Authorizer decides whether an identity may perform an action in a scope.
// It returns nil to allow, ErrForbidden to deny.
type Authorizer interface {
	Authorize(ctx context.Context, id auth.Identity, action Action, scope Scope) error
}

// DenyAll is an Authorizer that denies everything.
type DenyAll struct{}

// Authorize always returns ErrForbidden.
func (DenyAll) Authorize(context.Context, auth.Identity, Action, Scope) error { return ErrForbidden }

// identity is the authenticated caller of a use case.
type identity = auth.Identity

// caller returns the authenticated identity or ErrUnauthorized.
func caller(ctx context.Context) (auth.Identity, error) {
	id, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Identity{}, ErrUnauthorized
	}
	return id, nil
}

// actorOf converts an identity into an event actor.
func actorOf(id auth.Identity) event.Actor {
	kind := event.ActorHuman
	if id.Kind == auth.KindService {
		kind = event.ActorService
	}
	return event.Actor{Kind: kind, Subject: id.Subject}
}
