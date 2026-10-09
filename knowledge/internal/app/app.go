// Package app holds Knowledge's use cases. Authorization comes entirely
// from the caller's token (ADR-0022): its organization must match the
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

// Store persists entries of organization spaces.
type Store interface {
	Create(ctx context.Context, e domain.Entry) error
	Update(ctx context.Context, e domain.Entry, expected int64) error
	Get(ctx context.Context, organization, id string) (domain.Entry, error)
	List(ctx context.Context, organization string, f Filter) ([]domain.Entry, error)
	Versions(ctx context.Context, organization, id string) ([]domain.Version, error)
}

// ChangeListener is notified of changed entries (search indexing); optional.
type ChangeListener interface {
	EntryChanged(e domain.Entry)
}

// Service implements knowledge use cases.
type Service struct {
	Store     Store
	Searcher  SearchStore
	Embedders Embedders // nil: full-text search only
	// MaxDistance for semantic matches (default DefaultMaxDistance).
	MaxDistance float64
	Indexer     ChangeListener
	Now         func() time.Time
	NewID       func() string
}

// authorize checks the token in ctx against organization and capability and
// returns the author to record (the human behind Core, or the workload).
func authorize(ctx context.Context, organization, capability string) (string, error) {
	c, ok := runtoken.ClaimsFromContext(ctx)
	if !ok {
		return "", ErrUnauthenticated
	}
	if c.Organization == "" || c.Organization != organization {
		return "", fmt.Errorf("%w: token is not scoped to organization %s", ErrForbidden, organization)
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

// Create adds an entry to organization's space.
func (s *Service) Create(ctx context.Context, organization string, in CreateInput) (domain.Entry, error) {
	author, err := authorize(ctx, organization, runtoken.CapKnowledgeWrite)
	if err != nil {
		return domain.Entry{}, err
	}
	now := s.Now()
	e := domain.Entry{
		ID: s.NewID(), Organization: organization, Kind: in.Kind, Title: in.Title, Body: in.Body,
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
func (s *Service) Update(ctx context.Context, organization, id string, in UpdateInput) (domain.Entry, error) {
	author, err := authorize(ctx, organization, runtoken.CapKnowledgeWrite)
	if err != nil {
		return domain.Entry{}, err
	}
	e, err := s.Store.Get(ctx, organization, id)
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
func (s *Service) Get(ctx context.Context, organization, id string) (domain.Entry, error) {
	if _, err := authorize(ctx, organization, runtoken.CapKnowledgeRead); err != nil {
		return domain.Entry{}, err
	}
	return s.Store.Get(ctx, organization, id)
}

// List returns entries of organization's space.
func (s *Service) List(ctx context.Context, organization string, f Filter) ([]domain.Entry, error) {
	if _, err := authorize(ctx, organization, runtoken.CapKnowledgeRead); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, organization, f)
}

// Versions returns an entry's versions, newest first.
func (s *Service) Versions(ctx context.Context, organization, id string) ([]domain.Version, error) {
	if _, err := authorize(ctx, organization, runtoken.CapKnowledgeRead); err != nil {
		return nil, err
	}
	return s.Store.Versions(ctx, organization, id)
}

func (s *Service) changed(e domain.Entry) {
	if s.Indexer != nil {
		s.Indexer.EntryChanged(e)
	}
}

// SearchQuery is a hybrid search request (the vector is filled by the
// service).
type SearchQuery struct {
	Text    string
	Kind    domain.Kind
	Project string
	Limit   int
	Vector  []float32 // embedding of Text; nil: full-text only
	Model   string    // embedding model of Vector
	// MaxDistance drops semantic candidates farther than this cosine
	// distance (0 same direction … 2 opposite).
	MaxDistance float64
}

// DefaultMaxDistance suits the hash embedder; tune per embedding model.
const DefaultMaxDistance = 0.85

// SearchHit is a matching entry with its fused score.
type SearchHit struct {
	Entry domain.Entry
	Score float64
}
