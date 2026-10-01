package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/skill"
)

// SkillStore persists skills.
type SkillStore interface {
	CreateSkill(ctx context.Context, sk skill.Skill, customerID string, e event.Event) error
	UpdateSkillDraft(ctx context.Context, sk skill.Skill, expectedVersion int64, e event.Event) error
	PublishSkill(ctx context.Context, sk skill.Skill, v skill.Version, expectedVersion int64, e event.Event) error
	Skill(ctx context.Context, id string) (skill.Skill, error)
	ListSkills(ctx context.Context, scope skill.Scope) ([]skill.Skill, error)
	SkillsInScopes(ctx context.Context, scopes []skill.Scope) ([]skill.Skill, error)
	SkillVersions(ctx context.Context, skillID string) ([]skill.Version, error)
	SkillVersion(ctx context.Context, skillID string, number int64) (skill.Version, error)
	SetSkillPin(ctx context.Context, projectID string, p skill.Pin, e event.Event) error
	DeleteSkillPin(ctx context.Context, projectID, name string, e event.Event) error
	SkillPins(ctx context.Context, projectID string) ([]skill.Pin, error)
}

// Skills implements the skill registry (ADR-0010).
type Skills struct {
	Store   SkillStore
	Tenancy TenancyStore
	Authz   Authorizer
	Now     func() time.Time
	NewID   func() string
}

// target is the authorization target of a skill scope.
func skillTarget(s skill.Scope) Scope { return Scope{Customer: s.Customer, Project: s.Project} }

// resolveScope parses a scope, checks it exists, fills in a project's
// customer and returns the customer's ID ("" for organization scope).
func (sk *Skills) resolveScope(ctx context.Context, raw string) (skill.Scope, string, error) {
	sc, err := skill.ParseScope(raw)
	if err != nil {
		return skill.Scope{}, "", invalid(err)
	}
	switch sc.Kind {
	case skill.ScopeCustomer:
		c, err := sk.Tenancy.CustomerByKey(ctx, sc.Customer)
		if err != nil {
			return skill.Scope{}, "", err
		}
		return sc, c.ID, nil
	case skill.ScopeProject:
		p, err := sk.Tenancy.ProjectByKey(ctx, sc.Project)
		if err != nil {
			return skill.Scope{}, "", err
		}
		c, err := sk.Tenancy.CustomerByID(ctx, p.CustomerID)
		if err != nil {
			return skill.Scope{}, "", err
		}
		sc.Customer = c.Key
		return sc, c.ID, nil
	}
	return sc, "", nil
}

func (sk *Skills) authorize(ctx context.Context, action Action, scope skill.Scope) (identity, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, err
	}
	return id, sk.Authz.Authorize(ctx, id, action, skillTarget(scope))
}

func (sk *Skills) customerID(ctx context.Context, scope skill.Scope) string {
	if scope.Customer == "" {
		return ""
	}
	c, err := sk.Tenancy.CustomerByKey(ctx, scope.Customer)
	if err != nil {
		return ""
	}
	return c.ID
}

func skillEvent(s skill.Skill, customerID, typ string, actor event.Actor, payload map[string]any) event.Event {
	return event.Event{Customer: customerID, EntityType: "skill", EntityID: s.ID, Type: typ, Actor: actor,
		OccurredAt: s.UpdatedAt, Payload: mustJSON(payload)}
}

// CreateSkill creates a skill with its first draft.
func (sk *Skills) CreateSkill(ctx context.Context, scope, name string, draft skill.Content) (skill.Skill, error) {
	sc, customerID, err := sk.resolveScope(ctx, scope)
	if err != nil {
		return skill.Skill{}, err
	}
	id, err := sk.authorize(ctx, ActSkillWrite, sc)
	if err != nil {
		return skill.Skill{}, err
	}
	if err := firstErr(skill.ValidateName(name), draft.Validate()); err != nil {
		return skill.Skill{}, invalid(err)
	}
	now := sk.Now()
	s := skill.Skill{ID: sk.NewID(), Scope: sc, Name: name, Draft: draft, CreatedAt: now, UpdatedAt: now, Version: 1}
	e := skillEvent(s, customerID, "skill.created", actorOf(id), map[string]any{"name": name, "scope": sc.String()})
	if err := sk.Store.CreateSkill(ctx, s, customerID, e); err != nil {
		return skill.Skill{}, err
	}
	return s, nil
}

// GetSkill returns a skill with its draft.
func (sk *Skills) GetSkill(ctx context.Context, skillID string) (skill.Skill, error) {
	s, err := sk.Store.Skill(ctx, skillID)
	if err != nil {
		return skill.Skill{}, err
	}
	if _, err := sk.authorize(ctx, ActSkillRead, s.Scope); err != nil {
		return skill.Skill{}, err
	}
	return s, nil
}

// ListSkills returns the skills defined at exactly one scope.
func (sk *Skills) ListSkills(ctx context.Context, scope string) ([]skill.Skill, error) {
	sc, _, err := sk.resolveScope(ctx, scope)
	if err != nil {
		return nil, err
	}
	if _, err := sk.authorize(ctx, ActSkillRead, sc); err != nil {
		return nil, err
	}
	list, err := sk.Store.ListSkills(ctx, sc)
	if list == nil {
		list = []skill.Skill{}
	}
	return list, err
}

// UpdateDraftInput changes the non-nil draft fields.
type UpdateDraftInput struct {
	Version     int64
	Description *string
	Body        *string
	Files       *map[string]string
}

// UpdateDraft edits a skill's draft; published versions are unaffected.
func (sk *Skills) UpdateDraft(ctx context.Context, skillID string, in UpdateDraftInput) (skill.Skill, error) {
	s, err := sk.Store.Skill(ctx, skillID)
	if err != nil {
		return skill.Skill{}, err
	}
	id, err := sk.authorize(ctx, ActSkillWrite, s.Scope)
	if err != nil {
		return skill.Skill{}, err
	}
	if in.Description != nil {
		s.Draft.Description = *in.Description
	}
	if in.Body != nil {
		s.Draft.Body = *in.Body
	}
	if in.Files != nil {
		s.Draft.Files = maps.Clone(*in.Files)
	}
	if err := s.Draft.Validate(); err != nil {
		return skill.Skill{}, invalid(err)
	}
	s.UpdatedAt, s.Version = sk.Now(), in.Version+1
	e := skillEvent(s, sk.customerID(ctx, s.Scope), "skill.updated", actorOf(id), map[string]any{"name": s.Name})
	if err := sk.Store.UpdateSkillDraft(ctx, s, in.Version, e); err != nil {
		return skill.Skill{}, err
	}
	return s, nil
}

// Publish snapshots the draft as the next immutable version.
func (sk *Skills) Publish(ctx context.Context, skillID string, expectedVersion int64) (skill.Version, error) {
	s, err := sk.Store.Skill(ctx, skillID)
	if err != nil {
		return skill.Version{}, err
	}
	id, err := sk.authorize(ctx, ActSkillWrite, s.Scope)
	if err != nil {
		return skill.Version{}, err
	}
	if err := s.Draft.Validate(); err != nil {
		return skill.Version{}, invalid(err)
	}
	now := sk.Now()
	v := skill.Version{Number: s.LatestVersion + 1, Content: s.Draft, PublishedBy: id.Subject, PublishedAt: now}
	s.LatestVersion, s.UpdatedAt, s.Version = v.Number, now, expectedVersion+1
	e := skillEvent(s, sk.customerID(ctx, s.Scope), "skill.published", actorOf(id),
		map[string]any{"name": s.Name, "number": v.Number})
	if err := sk.Store.PublishSkill(ctx, s, v, expectedVersion, e); err != nil {
		return skill.Version{}, err
	}
	return v, nil
}

// Versions returns a skill's published versions, newest first.
func (sk *Skills) Versions(ctx context.Context, skillID string) ([]skill.Version, error) {
	if _, err := sk.GetSkill(ctx, skillID); err != nil {
		return nil, err
	}
	return sk.Store.SkillVersions(ctx, skillID)
}

// Version returns one published version.
func (sk *Skills) Version(ctx context.Context, skillID string, number int64) (skill.Version, error) {
	if _, err := sk.GetSkill(ctx, skillID); err != nil {
		return skill.Version{}, err
	}
	if number < 1 {
		return skill.Version{}, fmt.Errorf("%w: version numbers start at 1", ErrInvalid)
	}
	return sk.Store.SkillVersion(ctx, skillID, number)
}

// projectScope loads a project and its skill scope chain.
func (sk *Skills) projectScope(ctx context.Context, projectKey string) (string, string, []skill.Scope, error) {
	p, err := sk.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return "", "", nil, err
	}
	c, err := sk.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return "", "", nil, err
	}
	chain := []skill.Scope{
		{Kind: skill.ScopeOrganization},
		{Kind: skill.ScopeCustomer, Customer: c.Key},
		{Kind: skill.ScopeProject, Customer: c.Key, Project: p.Key},
	}
	return p.ID, c.ID, chain, nil
}

// Resolve returns a project's effective skills (organization → customer →
// project, pins applied). Requires skill.read on the project.
func (sk *Skills) Resolve(ctx context.Context, projectKey string) ([]skill.Resolved, error) {
	projectID, _, chain, err := sk.projectScope(ctx, projectKey)
	if err != nil {
		return nil, err
	}
	if _, err := sk.authorize(ctx, ActSkillRead, chain[2]); err != nil {
		return nil, err
	}
	all, err := sk.Store.SkillsInScopes(ctx, chain)
	if err != nil {
		return nil, err
	}
	pins, err := sk.Store.SkillPins(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return skill.Resolve(all, pins), nil
}

// SetPin pins a skill name for a project (version 0 = latest) or excludes
// it. Requires skill.write on the project.
func (sk *Skills) SetPin(ctx context.Context, projectKey string, p skill.Pin) error {
	projectID, customerID, chain, err := sk.projectScope(ctx, projectKey)
	if err != nil {
		return err
	}
	id, err := sk.authorize(ctx, ActSkillWrite, chain[2])
	if err != nil {
		return err
	}
	if err := skill.ValidateName(p.Name); err != nil {
		return invalid(err)
	}
	if p.Version < 0 {
		return fmt.Errorf("%w: version must be 0 (latest) or a published version", ErrInvalid)
	}
	e := event.Event{Customer: customerID, Project: projectID, EntityType: "project", EntityID: projectID,
		Type: "project.skill_pinned", Actor: actorOf(id), OccurredAt: sk.Now(),
		Payload: mustJSON(map[string]any{"name": p.Name, "version": p.Version, "disabled": p.Disabled})}
	return sk.Store.SetSkillPin(ctx, projectID, p, e)
}

// DeletePin removes a project's pin for a skill name.
func (sk *Skills) DeletePin(ctx context.Context, projectKey, name string) error {
	projectID, customerID, chain, err := sk.projectScope(ctx, projectKey)
	if err != nil {
		return err
	}
	id, err := sk.authorize(ctx, ActSkillWrite, chain[2])
	if err != nil {
		return err
	}
	e := event.Event{Customer: customerID, Project: projectID, EntityType: "project", EntityID: projectID,
		Type: "project.skill_unpinned", Actor: actorOf(id), OccurredAt: sk.Now(),
		Payload: mustJSON(map[string]any{"name": name})}
	err = sk.Store.DeleteSkillPin(ctx, projectID, name, e)
	if errors.Is(err, ErrConflict) {
		return fmt.Errorf("%w: no pin for %s", ErrNotFound, name)
	}
	return err
}
