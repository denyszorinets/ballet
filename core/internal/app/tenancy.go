package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

// TenancyStore persists organizations and projects. Writes take the event to
// record atomically with the change. Updates use optimistic concurrency on
// the entity's Version and return ErrConflict on a stale version.
type TenancyStore interface {
	CreateOrganization(ctx context.Context, c tenancy.Organization, e event.Event) error
	UpdateOrganization(ctx context.Context, c tenancy.Organization, expectedVersion int64, e event.Event) error
	OrganizationByKey(ctx context.Context, key string) (tenancy.Organization, error)
	OrganizationByID(ctx context.Context, id string) (tenancy.Organization, error)
	ListOrganizations(ctx context.Context) ([]tenancy.Organization, error)

	CreateProject(ctx context.Context, p tenancy.Project, e event.Event) error
	UpdateProject(ctx context.Context, p tenancy.Project, expectedVersion int64, e event.Event) error
	ProjectByKey(ctx context.Context, key string) (tenancy.Project, error)
	ProjectByID(ctx context.Context, id string) (tenancy.Project, error)
	ListProjects(ctx context.Context, organizationID string) ([]tenancy.Project, error)
}

// Tenancy implements organization and project use cases.
type Tenancy struct {
	Store TenancyStore
	Authz Authorizer
	Now   func() time.Time
	NewID func() string
}

// CreateOrganizationInput is the input of CreateOrganization.
type CreateOrganizationInput struct {
	Key  string
	Name string
}

// CreateOrganization creates an organization. Platform-level action.
func (t *Tenancy) CreateOrganization(ctx context.Context, in CreateOrganizationInput) (tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return tenancy.Organization{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActOrganizationCreate, Scope{}); err != nil {
		return tenancy.Organization{}, err
	}
	if err := firstErr(tenancy.ValidateOrganizationKey(in.Key), tenancy.ValidateName(in.Name)); err != nil {
		return tenancy.Organization{}, invalid(err)
	}
	now := t.Now()
	c := tenancy.Organization{ID: t.NewID(), Key: in.Key, Name: in.Name, FeaturePolicy: string(feature.PolicyDirect),
		CreatedAt: now, UpdatedAt: now, Version: 1}
	e := organizationEvent(c, "organization.created", actorOf(id), map[string]any{"key": c.Key, "name": c.Name})
	if err := t.Store.CreateOrganization(ctx, c, e); err != nil {
		return tenancy.Organization{}, err
	}
	return c, nil
}

// GetOrganization returns an organization by key.
func (t *Tenancy) GetOrganization(ctx context.Context, key string) (tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return tenancy.Organization{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActOrganizationRead, Scope{Organization: key}); err != nil {
		return tenancy.Organization{}, err
	}
	return t.Store.OrganizationByKey(ctx, key)
}

// ListOrganizations returns the organizations the caller may read.
func (t *Tenancy) ListOrganizations(ctx context.Context) ([]tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	all, err := t.Store.ListOrganizations(ctx)
	if err != nil {
		return nil, err
	}
	visible := []tenancy.Organization{}
	for _, c := range all {
		if t.Authz.Authorize(ctx, id, ActOrganizationRead, Scope{Organization: c.Key}) == nil {
			visible = append(visible, c)
		}
	}
	return visible, nil
}

// UpdateOrganizationInput is the input of UpdateOrganization.
type UpdateOrganizationInput struct {
	Key           string
	Name          string // "": unchanged
	FeaturePolicy string // "": unchanged
	Version       int64  // version the caller last read
}

// UpdateOrganization renames an organization or changes its feature
// policy.
func (t *Tenancy) UpdateOrganization(ctx context.Context, in UpdateOrganizationInput) (tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return tenancy.Organization{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActOrganizationUpdate, Scope{Organization: in.Key}); err != nil {
		return tenancy.Organization{}, err
	}
	if in.Name == "" && in.FeaturePolicy == "" {
		return tenancy.Organization{}, invalid(errors.New("give a name or a feature policy"))
	}
	changed := map[string]any{}
	if in.Name != "" {
		if err := tenancy.ValidateName(in.Name); err != nil {
			return tenancy.Organization{}, invalid(err)
		}
		changed["name"] = in.Name
	}
	if in.FeaturePolicy != "" {
		if err := feature.ValidatePolicy(feature.Policy(in.FeaturePolicy), false); err != nil {
			return tenancy.Organization{}, invalid(err)
		}
		changed["feature_policy"] = in.FeaturePolicy
	}
	c, err := t.Store.OrganizationByKey(ctx, in.Key)
	if err != nil {
		return tenancy.Organization{}, err
	}
	if in.Name != "" {
		c.Name = in.Name
	}
	if in.FeaturePolicy != "" {
		c.FeaturePolicy = in.FeaturePolicy
	}
	c.UpdatedAt, c.Version = t.Now(), in.Version+1
	e := organizationEvent(c, "organization.updated", actorOf(id), changed)
	if err := t.Store.UpdateOrganization(ctx, c, in.Version, e); err != nil {
		return tenancy.Organization{}, err
	}
	return c, nil
}

// CreateProjectInput is the input of CreateProject.
type CreateProjectInput struct {
	OrganizationKey string
	Key             string
	Name            string
	Description     string
}

// CreateProject creates a project for an organization.
func (t *Tenancy) CreateProject(ctx context.Context, in CreateProjectInput) (tenancy.Project, error) {
	id, err := caller(ctx)
	if err != nil {
		return tenancy.Project{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActProjectCreate, Scope{Organization: in.OrganizationKey}); err != nil {
		return tenancy.Project{}, err
	}
	if err := firstErr(tenancy.ValidateProjectKey(in.Key), tenancy.ValidateName(in.Name),
		tenancy.ValidateDescription(in.Description)); err != nil {
		return tenancy.Project{}, invalid(err)
	}
	c, err := t.Store.OrganizationByKey(ctx, in.OrganizationKey)
	if err != nil {
		return tenancy.Project{}, err
	}
	now := t.Now()
	p := tenancy.Project{
		ID: t.NewID(), OrganizationID: c.ID, Key: in.Key, Name: in.Name, Description: in.Description,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	e := projectEvent(p, "project.created", actorOf(id), map[string]any{"key": p.Key, "name": p.Name})
	if err := t.Store.CreateProject(ctx, p, e); err != nil {
		return tenancy.Project{}, err
	}
	return p, nil
}

// ProjectView is a project together with its organization's key.
type ProjectView struct {
	tenancy.Project
	OrganizationKey string
}

// GetProject returns a project by key.
func (t *Tenancy) GetProject(ctx context.Context, key string) (ProjectView, error) {
	id, err := caller(ctx)
	if err != nil {
		return ProjectView{}, err
	}
	p, c, err := t.projectWithOrganization(ctx, key)
	if err != nil {
		return ProjectView{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActProjectRead, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return ProjectView{}, err
	}
	return ProjectView{Project: p, OrganizationKey: c.Key}, nil
}

// ListProjects returns an organization's projects the caller may read.
func (t *Tenancy) ListProjects(ctx context.Context, organizationKey string) ([]ProjectView, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	c, err := t.Store.OrganizationByKey(ctx, organizationKey)
	if err != nil {
		return nil, err
	}
	all, err := t.Store.ListProjects(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	visible := []ProjectView{}
	for _, p := range all {
		if t.Authz.Authorize(ctx, id, ActProjectRead, Scope{Organization: c.Key, Project: p.Key}) == nil {
			visible = append(visible, ProjectView{Project: p, OrganizationKey: c.Key})
		}
	}
	return visible, nil
}

// UpdateProjectInput is the input of UpdateProject.
type UpdateProjectInput struct {
	Key         string
	Name        string
	Description string
	Version     int64
}

// UpdateProject changes a project's name and description.
func (t *Tenancy) UpdateProject(ctx context.Context, in UpdateProjectInput) (ProjectView, error) {
	id, err := caller(ctx)
	if err != nil {
		return ProjectView{}, err
	}
	p, c, err := t.projectWithOrganization(ctx, in.Key)
	if err != nil {
		return ProjectView{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActProjectUpdate, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return ProjectView{}, err
	}
	if err := firstErr(tenancy.ValidateName(in.Name), tenancy.ValidateDescription(in.Description)); err != nil {
		return ProjectView{}, invalid(err)
	}
	p.Name, p.Description, p.UpdatedAt, p.Version = in.Name, in.Description, t.Now(), in.Version+1
	e := projectEvent(p, "project.updated", actorOf(id), map[string]any{"name": p.Name})
	if err := t.Store.UpdateProject(ctx, p, in.Version, e); err != nil {
		return ProjectView{}, err
	}
	return ProjectView{Project: p, OrganizationKey: c.Key}, nil
}

func (t *Tenancy) projectWithOrganization(ctx context.Context, key string) (tenancy.Project, tenancy.Organization, error) {
	p, err := t.Store.ProjectByKey(ctx, key)
	if err != nil {
		return tenancy.Project{}, tenancy.Organization{}, err
	}
	c, err := t.Store.OrganizationByID(ctx, p.OrganizationID)
	if err != nil {
		return tenancy.Project{}, tenancy.Organization{}, err
	}
	return p, c, nil
}

func organizationEvent(c tenancy.Organization, typ string, actor event.Actor, payload map[string]any) event.Event {
	return event.Event{
		Organization: c.ID, EntityType: "organization", EntityID: c.ID, Type: typ, Actor: actor,
		OccurredAt: c.UpdatedAt, Payload: mustJSON(payload),
	}
}

func projectEvent(p tenancy.Project, typ string, actor event.Actor, payload map[string]any) event.Event {
	return event.Event{
		Organization: p.OrganizationID, Project: p.ID, EntityType: "project", EntityID: p.ID, Type: typ,
		Actor: actor, OccurredAt: p.UpdatedAt, Payload: mustJSON(payload),
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // payloads are maps of plain values
	}
	return b
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
