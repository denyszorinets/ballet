package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/credential"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

// CredentialStore persists encrypted credentials.
type CredentialStore interface {
	SetCredential(ctx context.Context, c credential.Credential, ciphertext string, e event.Event) error
	DeleteCredential(ctx context.Context, customerID, projectID string, p credential.Provider, e event.Event) error
	ListCredentials(ctx context.Context, customerID string) ([]credential.Credential, error)
	CredentialCiphertext(ctx context.Context, customerID, projectID string, p credential.Provider) (credential.Credential, string, error)
}

// Sealer encrypts secrets at rest.
type Sealer interface {
	Seal(plaintext, aad string) (string, error)
	Open(sealed, aad string) (string, error)
}

// Credentials manages LLM provider credentials. Secrets can be written
// through the API but are only ever returned by Resolve (for the gateway).
type Credentials struct {
	Store   CredentialStore
	Tenancy TenancyStore
	Authz   Authorizer
	Box     Sealer
	Now     func() time.Time
	NewID   func() string
}

// CredentialView is a credential without its secret.
type CredentialView struct {
	credential.Credential
	CustomerKey string
	ProjectKey  string // empty: customer default
}

// aad binds a ciphertext to its scope.
func aad(customerID, projectID string, p credential.Provider) string {
	return customerID + "/" + projectID + "/" + string(p)
}

// scope resolves customer (and optional project) keys and authorizes
// credential.manage.
func (cr *Credentials) scope(ctx context.Context, customerKey, projectKey string) (identity, tenancy.Customer, tenancy.Project, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, tenancy.Customer{}, tenancy.Project{}, err
	}
	var p tenancy.Project
	if projectKey != "" {
		if p, err = cr.Tenancy.ProjectByKey(ctx, projectKey); err != nil {
			return identity{}, tenancy.Customer{}, tenancy.Project{}, err
		}
	}
	var c tenancy.Customer
	if customerKey != "" {
		c, err = cr.Tenancy.CustomerByKey(ctx, customerKey)
	} else {
		c, err = cr.Tenancy.CustomerByID(ctx, p.CustomerID)
	}
	if err != nil {
		return identity{}, tenancy.Customer{}, tenancy.Project{}, err
	}
	if projectKey != "" && p.CustomerID != c.ID {
		return identity{}, tenancy.Customer{}, tenancy.Project{}, fmt.Errorf("%w: project %s", ErrNotFound, projectKey)
	}
	if err := cr.Authz.Authorize(ctx, id, ActCredentialManage, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return identity{}, tenancy.Customer{}, tenancy.Project{}, err
	}
	return id, c, p, nil
}

// SetCredentialInput sets a customer default (ProjectKey empty) or a
// project override. Exactly one of CustomerKey and ProjectKey is set.
type SetCredentialInput struct {
	CustomerKey string
	ProjectKey  string
	Provider    credential.Provider
	APIKey      string
	BaseURL     string
}

// Set stores (or replaces) a credential.
func (cr *Credentials) Set(ctx context.Context, in SetCredentialInput) (CredentialView, error) {
	id, c, p, err := cr.scope(ctx, in.CustomerKey, in.ProjectKey)
	if err != nil {
		return CredentialView{}, err
	}
	if err := credential.Validate(in.Provider, in.APIKey, in.BaseURL); err != nil {
		return CredentialView{}, invalid(err)
	}
	sealed, err := cr.Box.Seal(in.APIKey, aad(c.ID, p.ID, in.Provider))
	if err != nil {
		return CredentialView{}, err
	}
	now := cr.Now()
	cred := credential.Credential{
		ID: cr.NewID(), CustomerID: c.ID, ProjectID: p.ID, Provider: in.Provider, BaseURL: in.BaseURL,
		Fingerprint: credential.Fingerprint(in.APIKey), CreatedAt: now, UpdatedAt: now,
	}
	e := event.Event{
		Customer: c.ID, Project: p.ID, EntityType: "credential", EntityID: aad(c.ID, p.ID, in.Provider),
		Type: "credential.set", Actor: actorOf(id), OccurredAt: now,
		Payload: mustJSON(map[string]any{"provider": in.Provider, "project": p.Key, "fingerprint": cred.Fingerprint}),
	}
	if err := cr.Store.SetCredential(ctx, cred, sealed, e); err != nil {
		return CredentialView{}, err
	}
	return CredentialView{Credential: cred, CustomerKey: c.Key, ProjectKey: p.Key}, nil
}

// Delete removes a credential.
func (cr *Credentials) Delete(ctx context.Context, customerKey, projectKey string, provider credential.Provider) error {
	id, c, p, err := cr.scope(ctx, customerKey, projectKey)
	if err != nil {
		return err
	}
	e := event.Event{
		Customer: c.ID, Project: p.ID, EntityType: "credential", EntityID: aad(c.ID, p.ID, provider),
		Type: "credential.deleted", Actor: actorOf(id), OccurredAt: cr.Now(),
		Payload: mustJSON(map[string]any{"provider": provider, "project": p.Key}),
	}
	err = cr.Store.DeleteCredential(ctx, c.ID, p.ID, provider, e)
	if errors.Is(err, ErrConflict) {
		return fmt.Errorf("%w: no %s credential at this scope", ErrNotFound, provider)
	}
	return err
}

// List returns the customer's credentials (defaults and project overrides)
// without secrets.
func (cr *Credentials) List(ctx context.Context, customerKey string) ([]CredentialView, error) {
	_, c, _, err := cr.scope(ctx, customerKey, "")
	if err != nil {
		return nil, err
	}
	creds, err := cr.Store.ListCredentials(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	out := make([]CredentialView, 0, len(creds))
	for _, cred := range creds {
		v := CredentialView{Credential: cred, CustomerKey: c.Key}
		if cred.ProjectID != "" {
			p, err := cr.Tenancy.ProjectByID(ctx, cred.ProjectID)
			if err != nil {
				return nil, err
			}
			v.ProjectKey = p.Key
		}
		out = append(out, v)
	}
	return out, nil
}

// Resolve returns the decrypted credential for a project (its override,
// else the customer default) or, without a project, the customer default.
// It performs no RBAC check: callers must be services holding
// credentials.read.
func (cr *Credentials) Resolve(ctx context.Context, customerKey, projectKey string, provider credential.Provider) (credential.Credential, error) {
	c, err := cr.Tenancy.CustomerByKey(ctx, customerKey)
	if err != nil {
		return credential.Credential{}, err
	}
	candidates := []string{""}
	if projectKey != "" {
		p, err := cr.Tenancy.ProjectByKey(ctx, projectKey)
		if err != nil {
			return credential.Credential{}, err
		}
		if p.CustomerID != c.ID {
			return credential.Credential{}, fmt.Errorf("%w: project %s", ErrNotFound, projectKey)
		}
		candidates = []string{p.ID, ""}
	}
	for _, projectID := range candidates {
		cred, sealed, err := cr.Store.CredentialCiphertext(ctx, c.ID, projectID, provider)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return credential.Credential{}, err
		}
		if cred.APIKey, err = cr.Box.Open(sealed, aad(c.ID, projectID, provider)); err != nil {
			return credential.Credential{}, fmt.Errorf("resolve credential: %w", err)
		}
		return cred, nil
	}
	return credential.Credential{}, fmt.Errorf("%w: no %s credential for %s", ErrNotFound, provider, customerKey)
}
