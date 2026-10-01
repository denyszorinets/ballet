package app

import (
	"context"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/kit/auth"
)

// Action is an operation subject to authorization.
type Action string

// Actions.
const (
	ActCustomerCreate Action = "customer.create"
	ActCustomerRead   Action = "customer.read"
	ActCustomerUpdate Action = "customer.update"
	ActProjectCreate  Action = "project.create"
	ActProjectRead    Action = "project.read"
	ActProjectUpdate  Action = "project.update"
)

// Scope is where an action applies. Empty fields mean organization level.
type Scope struct {
	Customer string // customer key
	Project  string // project key
}

// Authorizer decides whether an identity may perform an action in a scope.
// It returns nil to allow, ErrForbidden to deny.
type Authorizer interface {
	Authorize(ctx context.Context, id auth.Identity, action Action, scope Scope) error
}

// DenyAll is an Authorizer that denies everything.
type DenyAll struct{}

// Authorize always returns ErrForbidden.
func (DenyAll) Authorize(context.Context, auth.Identity, Action, Scope) error { return ErrForbidden }

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
