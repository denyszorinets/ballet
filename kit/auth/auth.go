// Package auth carries the authenticated caller through request contexts.
// Authentication mechanisms (OIDC for humans, run tokens for agents and
// services) live in subpackages and produce an Identity.
package auth

import "context"

// Kind distinguishes human users from workloads.
type Kind string

// Identity kinds.
const (
	KindHuman   Kind = "human"
	KindService Kind = "service"
)

// Identity is an authenticated caller.
type Identity struct {
	Kind    Kind
	Subject string
	Email   string
	Name    string
	// Claims holds all token claims for authorization (e.g. groups).
	Claims map[string]any
}

type contextKey struct{}

// WithIdentity returns a context carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext returns the identity in ctx, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(contextKey{}).(Identity)
	return id, ok
}
