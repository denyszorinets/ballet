package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

// ExecutionStore persists projects' execution settings.
type ExecutionStore interface {
	ExecutionSettings(ctx context.Context, projectID string) (execution.Settings, error)
	SetExecutionSettings(ctx context.Context, x execution.Settings, expectedVersion int64, e event.Event) error
}

// ExecutionView is a project's execution settings with its key.
type ExecutionView struct {
	execution.Settings
	ProjectKey string
}

// Execution implements the execution settings use cases.
type Execution struct {
	Store   ExecutionStore
	Tenancy TenancyStore
	Authz   Authorizer
	Now     func() time.Time
}

// Get returns a project's execution settings; never-set settings come
// back empty with version 0. Requires tracker.read.
func (ex *Execution) Get(ctx context.Context, projectKey string) (ExecutionView, error) {
	_, p, _, err := ex.authorize(ctx, projectKey, ActTrackerRead)
	if err != nil {
		return ExecutionView{}, err
	}
	x, err := ex.Store.ExecutionSettings(ctx, p.ID)
	if errors.Is(err, ErrNotFound) {
		x = execution.Settings{ProjectID: p.ID, BranchTemplate: execution.DefaultBranchTemplate}
	} else if err != nil {
		return ExecutionView{}, err
	}
	return ExecutionView{Settings: x, ProjectKey: p.Key}, nil
}

// Set replaces a project's execution settings if they are at version (0
// for the first time). Requires project.update.
func (ex *Execution) Set(ctx context.Context, projectKey string, in execution.Settings, version int64) (ExecutionView, error) {
	id, p, c, err := ex.authorize(ctx, projectKey, ActProjectUpdate)
	if err != nil {
		return ExecutionView{}, err
	}
	if in.BranchTemplate == "" {
		in.BranchTemplate = execution.DefaultBranchTemplate
	}
	if err := in.Validate(); err != nil {
		return ExecutionView{}, invalid(err)
	}
	in.ProjectID, in.UpdatedAt, in.Version = p.ID, ex.Now(), version+1
	e := event.Event{Customer: c.ID, Project: p.ID, EntityType: "project", EntityID: p.ID, Type: "project.execution_updated",
		Actor: actorIn(ctx, id), OccurredAt: in.UpdatedAt, Payload: mustJSON(map[string]any{"repo_url": in.RepoURL})}
	if err := ex.Store.SetExecutionSettings(ctx, in, version, e); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			return ExecutionView{}, fmt.Errorf("%w: settings changed meanwhile; reload", ErrConflict)
		}
		return ExecutionView{}, err
	}
	return ExecutionView{Settings: in, ProjectKey: p.Key}, nil
}

func (ex *Execution) authorize(ctx context.Context, projectKey string, a Action) (identity, tenancy.Project, tenancy.Customer, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	p, err := ex.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	c, err := ex.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	if err := ex.Authz.Authorize(ctx, id, a, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return identity{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	return id, p, c, nil
}
