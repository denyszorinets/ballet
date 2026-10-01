// Package app holds Knowledge's use cases. Authorization comes entirely
// from the caller's token (ADR-0022): its customer must match the
// knowledge space, and it must carry the required capability.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/knowledge/internal/domain"
)

// Errors returned by use cases.
var (
	ErrInvalid         = errors.New("invalid argument")
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflicting concurrent update")
	ErrForbidden       = errors.New("forbidden")
	ErrUnauthenticated = errors.New("unauthenticated")
)

// Filter selects entries.
type Filter struct {
	Kind    domain.Kind
	Project string
	Item    string
	Limit   int
}

// Store persists entries of customer spaces.
type Store interface {
	Create(ctx context.Context, e domain.Entry) error
	Update(ctx context.Context, e domain.Entry, expected int64) error
	Get(ctx context.Context, customer, id string) (domain.Entry, error)
	List(ctx context.Context, customer string, f Filter) ([]domain.Entry, error)
	Versions(ctx context.Context, customer, id string) ([]domain.Version, error)
}

// Indexer is notified of changed entries (search indexing); optional.
type Indexer interface {
	EntryChanged(e domain.Entry)
}

// Service implements knowledge use cases.
type Service struct {
	Store   Store
	Indexer Indexer
	Now     func() time.Time
	NewID   func() string
}

// authorize checks the token in ctx against customer and capability and
// returns the author to record (the human behind Core, or the workload).
func authorize(ctx context.Context, customer, capability string) (string, error) {
	c, ok := runtoken.ClaimsFromContext(ctx)
	if !ok {
		return "", ErrUnauthenticated
	}
	if c.Customer == "" || c.Customer != customer {
		return "", fmt.Errorf("%w: token is not scoped to customer %s", ErrForbidden, customer)
	}
	if !c.Can(capability) {
		return "", fmt.Errorf("%w: missing %s", ErrForbidden, capability)
	}
	if c.ActingFor != "" {
		return c.ActingFor, nil
	}
	return c.Subject, nil
}

// CreateInput is the input of Create.
type CreateInput struct {
	Kind     domain.Kind
	Title    string
	Body     string
	Projects []string
	Items    []string
}

// Create adds an entry to customer's space.
func (s *Service) Create(ctx context.Context, customer string, in CreateInput) (domain.Entry, error) {
	author, err := authorize(ctx, customer, runtoken.CapKnowledgeWrite)
	if err != nil {
		return domain.Entry{}, err
	}
	now := s.Now()
	e := domain.Entry{
		ID: s.NewID(), Customer: customer, Kind: in.Kind, Title: in.Title, Body: in.Body,
		Projects: in.Projects, Items: in.Items, Version: 1, CreatedBy: author, UpdatedBy: author,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Validate(); err != nil {
		return domain.Entry{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := s.Store.Create(ctx, e); err != nil {
		return domain.Entry{}, err
	}
	s.changed(e)
	return e, nil
}

// UpdateInput changes the non-nil fields of an entry.
type UpdateInput struct {
	Version  int64
	Kind     *domain.Kind
	Title    *string
	Body     *string
	Projects *[]string
	Items    *[]string
}

// Update edits an entry, creating a new version.
func (s *Service) Update(ctx context.Context, customer, id string, in UpdateInput) (domain.Entry, error) {
	author, err := authorize(ctx, customer, runtoken.CapKnowledgeWrite)
	if err != nil {
		return domain.Entry{}, err
	}
	e, err := s.Store.Get(ctx, customer, id)
	if err != nil {
		return domain.Entry{}, err
	}
	if in.Kind != nil {
		e.Kind = *in.Kind
	}
	if in.Title != nil {
		e.Title = *in.Title
	}
	if in.Body != nil {
		e.Body = *in.Body
	}
	if in.Projects != nil {
		e.Projects = *in.Projects
	}
	if in.Items != nil {
		e.Items = *in.Items
	}
	if err := e.Validate(); err != nil {
		return domain.Entry{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	e.Version, e.UpdatedBy, e.UpdatedAt = in.Version+1, author, s.Now()
	if err := s.Store.Update(ctx, e, in.Version); err != nil {
		return domain.Entry{}, err
	}
	s.changed(e)
	return e, nil
}

// Get returns an entry.
func (s *Service) Get(ctx context.Context, customer, id string) (domain.Entry, error) {
	if _, err := authorize(ctx, customer, runtoken.CapKnowledgeRead); err != nil {
		return domain.Entry{}, err
	}
	return s.Store.Get(ctx, customer, id)
}

// List returns entries of customer's space.
func (s *Service) List(ctx context.Context, customer string, f Filter) ([]domain.Entry, error) {
	if _, err := authorize(ctx, customer, runtoken.CapKnowledgeRead); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, customer, f)
}

// Versions returns an entry's versions, newest first.
func (s *Service) Versions(ctx context.Context, customer, id string) ([]domain.Version, error) {
	if _, err := authorize(ctx, customer, runtoken.CapKnowledgeRead); err != nil {
		return nil, err
	}
	return s.Store.Versions(ctx, customer, id)
}

func (s *Service) changed(e domain.Entry) {
	if s.Indexer != nil {
		s.Indexer.EntryChanged(e)
	}
}
