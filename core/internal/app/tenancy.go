package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

// TenancyStore persists customers and projects. Writes take the event to
// record atomically with the change. Updates use optimistic concurrency on
// the entity's Version and return ErrConflict on a stale version.
type TenancyStore interface {
	CreateCustomer(ctx context.Context, c tenancy.Customer, e event.Event) error
	UpdateCustomer(ctx context.Context, c tenancy.Customer, expectedVersion int64, e event.Event) error
	CustomerByKey(ctx context.Context, key string) (tenancy.Customer, error)
	CustomerByID(ctx context.Context, id string) (tenancy.Customer, error)
	ListCustomers(ctx context.Context) ([]tenancy.Customer, error)

	CreateProject(ctx context.Context, p tenancy.Project, e event.Event) error
	UpdateProject(ctx context.Context, p tenancy.Project, expectedVersion int64, e event.Event) error
	ProjectByKey(ctx context.Context, key string) (tenancy.Project, error)
	ProjectByID(ctx context.Context, id string) (tenancy.Project, error)
	ListProjects(ctx context.Context, customerID string) ([]tenancy.Project, error)
}

// Tenancy implements customer and project use cases.
type Tenancy struct {
	Store TenancyStore
	Authz Authorizer
	Now   func() time.Time
	NewID func() string
}

// CreateCustomerInput is the input of CreateCustomer.
type CreateCustomerInput struct {
	Key  string
	Name string
}

// CreateCustomer creates a customer. Organization-level action.
func (t *Tenancy) CreateCustomer(ctx context.Context, in CreateCustomerInput) (tenancy.Customer, error) {
	id, err := caller(ctx)
	if err != nil {
		return tenancy.Customer{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActCustomerCreate, Scope{}); err != nil {
		return tenancy.Customer{}, err
	}
	if err := firstErr(tenancy.ValidateCustomerKey(in.Key), tenancy.ValidateName(in.Name)); err != nil {
		return tenancy.Customer{}, invalid(err)
	}
	now := t.Now()
	c := tenancy.Customer{ID: t.NewID(), Key: in.Key, Name: in.Name, CreatedAt: now, UpdatedAt: now, Version: 1}
	e := customerEvent(c, "customer.created", actorOf(id), map[string]any{"key": c.Key, "name": c.Name})
	if err := t.Store.CreateCustomer(ctx, c, e); err != nil {
		return tenancy.Customer{}, err
	}
	return c, nil
}

// GetCustomer returns a customer by key.
func (t *Tenancy) GetCustomer(ctx context.Context, key string) (tenancy.Customer, error) {
	id, err := caller(ctx)
	if err != nil {
		return tenancy.Customer{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActCustomerRead, Scope{Customer: key}); err != nil {
		return tenancy.Customer{}, err
	}
	return t.Store.CustomerByKey(ctx, key)
}

// ListCustomers returns the customers the caller may read.
func (t *Tenancy) ListCustomers(ctx context.Context) ([]tenancy.Customer, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	all, err := t.Store.ListCustomers(ctx)
	if err != nil {
		return nil, err
	}
	visible := []tenancy.Customer{}
	for _, c := range all {
		if t.Authz.Authorize(ctx, id, ActCustomerRead, Scope{Customer: c.Key}) == nil {
			visible = append(visible, c)
		}
	}
	return visible, nil
}

// UpdateCustomerInput is the input of UpdateCustomer.
type UpdateCustomerInput struct {
	Key     string
	Name    string
	Version int64 // version the caller last read
}

// UpdateCustomer renames a customer.
func (t *Tenancy) UpdateCustomer(ctx context.Context, in UpdateCustomerInput) (tenancy.Customer, error) {
	id, err := caller(ctx)
	if err != nil {
		return tenancy.Customer{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActCustomerUpdate, Scope{Customer: in.Key}); err != nil {
		return tenancy.Customer{}, err
	}
	if err := tenancy.ValidateName(in.Name); err != nil {
		return tenancy.Customer{}, invalid(err)
	}
	c, err := t.Store.CustomerByKey(ctx, in.Key)
	if err != nil {
		return tenancy.Customer{}, err
	}
	c.Name, c.UpdatedAt, c.Version = in.Name, t.Now(), in.Version+1
	e := customerEvent(c, "customer.updated", actorOf(id), map[string]any{"name": c.Name})
	if err := t.Store.UpdateCustomer(ctx, c, in.Version, e); err != nil {
		return tenancy.Customer{}, err
	}
	return c, nil
}

// CreateProjectInput is the input of CreateProject.
type CreateProjectInput struct {
	CustomerKey string
	Key         string
	Name        string
	Description string
}

// CreateProject creates a project for a customer.
func (t *Tenancy) CreateProject(ctx context.Context, in CreateProjectInput) (tenancy.Project, error) {
	id, err := caller(ctx)
	if err != nil {
		return tenancy.Project{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActProjectCreate, Scope{Customer: in.CustomerKey}); err != nil {
		return tenancy.Project{}, err
	}
	if err := firstErr(tenancy.ValidateProjectKey(in.Key), tenancy.ValidateName(in.Name),
		tenancy.ValidateDescription(in.Description)); err != nil {
		return tenancy.Project{}, invalid(err)
	}
	c, err := t.Store.CustomerByKey(ctx, in.CustomerKey)
	if err != nil {
		return tenancy.Project{}, err
	}
	now := t.Now()
	p := tenancy.Project{
		ID: t.NewID(), CustomerID: c.ID, Key: in.Key, Name: in.Name, Description: in.Description,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	e := projectEvent(p, "project.created", actorOf(id), map[string]any{"key": p.Key, "name": p.Name})
	if err := t.Store.CreateProject(ctx, p, e); err != nil {
		return tenancy.Project{}, err
	}
	return p, nil
}

// ProjectView is a project together with its customer's key.
type ProjectView struct {
	tenancy.Project
	CustomerKey string
}

// GetProject returns a project by key.
func (t *Tenancy) GetProject(ctx context.Context, key string) (ProjectView, error) {
	id, err := caller(ctx)
	if err != nil {
		return ProjectView{}, err
	}
	p, c, err := t.projectWithCustomer(ctx, key)
	if err != nil {
		return ProjectView{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActProjectRead, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return ProjectView{}, err
	}
	return ProjectView{Project: p, CustomerKey: c.Key}, nil
}

// ListProjects returns a customer's projects the caller may read.
func (t *Tenancy) ListProjects(ctx context.Context, customerKey string) ([]ProjectView, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	c, err := t.Store.CustomerByKey(ctx, customerKey)
	if err != nil {
		return nil, err
	}
	all, err := t.Store.ListProjects(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	visible := []ProjectView{}
	for _, p := range all {
		if t.Authz.Authorize(ctx, id, ActProjectRead, Scope{Customer: c.Key, Project: p.Key}) == nil {
			visible = append(visible, ProjectView{Project: p, CustomerKey: c.Key})
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
	p, c, err := t.projectWithCustomer(ctx, in.Key)
	if err != nil {
		return ProjectView{}, err
	}
	if err := t.Authz.Authorize(ctx, id, ActProjectUpdate, Scope{Customer: c.Key, Project: p.Key}); err != nil {
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
	return ProjectView{Project: p, CustomerKey: c.Key}, nil
}

func (t *Tenancy) projectWithCustomer(ctx context.Context, key string) (tenancy.Project, tenancy.Customer, error) {
	p, err := t.Store.ProjectByKey(ctx, key)
	if err != nil {
		return tenancy.Project{}, tenancy.Customer{}, err
	}
	c, err := t.Store.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return tenancy.Project{}, tenancy.Customer{}, err
	}
	return p, c, nil
}

func customerEvent(c tenancy.Customer, typ string, actor event.Actor, payload map[string]any) event.Event {
	return event.Event{
		Customer: c.ID, EntityType: "customer", EntityID: c.ID, Type: typ, Actor: actor,
		OccurredAt: c.UpdatedAt, Payload: mustJSON(payload),
	}
}

func projectEvent(p tenancy.Project, typ string, actor event.Actor, payload map[string]any) event.Event {
	return event.Event{
		Customer: p.CustomerID, Project: p.ID, EntityType: "project", EntityID: p.ID, Type: typ,
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
