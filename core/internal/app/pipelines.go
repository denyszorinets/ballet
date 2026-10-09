package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

// PipelineVersion is a stored version of a project's pipeline.
type PipelineVersion struct {
	ProjectID  string
	Name       string
	Version    int64 // 0: Ballet's default template, never saved
	Definition pipeline.Definition
	CreatedBy  string
	CreatedAt  time.Time
}

// PipelineStore persists pipeline versions.
type PipelineStore interface {
	SavePipelineVersion(ctx context.Context, p PipelineVersion, e event.Event) error
	PipelineVersion(ctx context.Context, projectID, name string, version int64) (PipelineVersion, error)
	PipelineVersions(ctx context.Context, projectID, name string) ([]PipelineVersion, error)
	LatestPipelines(ctx context.Context, projectID string) ([]PipelineVersion, error)
}

// DefaultPipeline names the pipeline of tickets whose type has none.
const DefaultPipeline = "default"

// Pipelines implements pipeline definitions per project (ADR-0017).
type Pipelines struct {
	Store    PipelineStore
	Tenancy  TenancyStore
	Authz    Authorizer
	Adapters []string // agent adapters stages may use
	Now      func() time.Time
}

// PipelineView is a pipeline version with its project's key.
type PipelineView struct {
	PipelineVersion
	ProjectKey string
}

func (ps *Pipelines) project(ctx context.Context, projectKey string, a Action) (identity, tenancy.Project, tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	p, err := ps.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	c, err := ps.Tenancy.OrganizationByID(ctx, p.OrganizationID)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	if err := ps.Authz.Authorize(ctx, id, a, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return identity{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	return id, p, c, nil
}

func template(projectID string) PipelineVersion {
	return PipelineVersion{ProjectID: projectID, Name: DefaultPipeline, Definition: pipeline.Default(), CreatedBy: "ballet"}
}

// List returns the latest version of each pipeline of a project; without a
// saved default pipeline, Ballet's template is listed as version 0.
func (ps *Pipelines) List(ctx context.Context, projectKey string) ([]PipelineView, error) {
	_, p, _, err := ps.project(ctx, projectKey, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	list, err := ps.Store.LatestPipelines(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := []PipelineView{}
	hasDefault := false
	for _, v := range list {
		hasDefault = hasDefault || v.Name == DefaultPipeline
		out = append(out, PipelineView{PipelineVersion: v, ProjectKey: p.Key})
	}
	if !hasDefault {
		out = append([]PipelineView{{PipelineVersion: template(p.ID), ProjectKey: p.Key}}, out...)
	}
	return out, nil
}

// Get returns a version of a pipeline (0: the latest).
func (ps *Pipelines) Get(ctx context.Context, projectKey, name string, version int64) (PipelineView, error) {
	_, p, _, err := ps.project(ctx, projectKey, ActTrackerRead)
	if err != nil {
		return PipelineView{}, err
	}
	v, err := ps.Store.PipelineVersion(ctx, p.ID, name, version)
	if errors.Is(err, ErrNotFound) && name == DefaultPipeline && version == 0 {
		v, err = template(p.ID), nil
	}
	if err != nil {
		return PipelineView{}, err
	}
	return PipelineView{PipelineVersion: v, ProjectKey: p.Key}, nil
}

// Versions returns a pipeline's versions, newest first.
func (ps *Pipelines) Versions(ctx context.Context, projectKey, name string) ([]PipelineView, error) {
	_, p, _, err := ps.project(ctx, projectKey, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	list, err := ps.Store.PipelineVersions(ctx, p.ID, name)
	if err != nil {
		return nil, err
	}
	out := make([]PipelineView, 0, len(list))
	for _, v := range list {
		out = append(out, PipelineView{PipelineVersion: v, ProjectKey: p.Key})
	}
	return out, nil
}

// Save stores a new version of a pipeline after the version the caller
// read (0 for the first). Requires project.update.
func (ps *Pipelines) Save(ctx context.Context, projectKey, name string, def pipeline.Definition, after int64) (PipelineView, error) {
	id, p, c, err := ps.project(ctx, projectKey, ActProjectUpdate)
	if err != nil {
		return PipelineView{}, err
	}
	if err := pipeline.ValidName(name); err != nil {
		return PipelineView{}, invalid(err)
	}
	if err := def.Validate(ps.Adapters); err != nil {
		return PipelineView{}, invalid(err)
	}
	v := PipelineVersion{ProjectID: p.ID, Name: name, Version: after + 1, Definition: def, CreatedBy: id.Subject, CreatedAt: ps.Now()}
	e := event.Event{Organization: c.ID, Project: p.ID, EntityType: "project", EntityID: p.ID, Type: "project.pipeline_saved",
		Actor: actorIn(ctx, id), OccurredAt: v.CreatedAt, Payload: mustJSON(map[string]any{"name": name, "version": v.Version})}
	if err := ps.Store.SavePipelineVersion(ctx, v, e); err != nil {
		if errors.Is(err, ErrConflict) {
			return PipelineView{}, fmt.Errorf("%w: pipeline %s changed meanwhile; reload", ErrConflict, name)
		}
		return PipelineView{}, err
	}
	return PipelineView{PipelineVersion: v, ProjectKey: p.Key}, nil
}

// ForTicket returns the pipeline a ticket of type ticketType starts on: the
// project's pipeline named after the type, else its default pipeline, else
// Ballet's template. For Core's orchestrator; no authorization.
func (ps *Pipelines) ForTicket(ctx context.Context, projectID, ticketType string) (PipelineVersion, error) {
	for _, name := range []string{ticketType, DefaultPipeline} {
		if name == "" {
			continue
		}
		v, err := ps.Store.PipelineVersion(ctx, projectID, name, 0)
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return PipelineVersion{}, err
		}
	}
	return template(projectID), nil
}
