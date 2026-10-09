package app

import (
	"context"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// ItemFilter selects items of a project. Zero fields do not filter.
type ItemFilter struct {
	Kind        tracker.Kind
	State       tracker.State
	EpicID      string
	MilestoneID string
}

// ItemStore persists milestones, epics and tickets.
type ItemStore interface {
	CreateItem(ctx context.Context, it tracker.Item, e event.Event) (tracker.Item, error)
	UpdateItem(ctx context.Context, it tracker.Item, expectedVersion int64, e event.Event) error
	ItemByID(ctx context.Context, id string) (tracker.Item, error)
	ItemByKey(ctx context.Context, key string) (tracker.Item, error)
	ListItems(ctx context.Context, projectID string, f ItemFilter) ([]tracker.Item, error)
}

// EventReader reads recorded events.
type EventReader interface {
	EntityHistory(ctx context.Context, entityType, entityID string) ([]event.Event, error)
}

// Tracker implements milestone, epic and ticket use cases.
type Tracker struct {
	Items   ItemStore
	Deps    DependencyStore
	Tenancy TenancyStore
	Events  EventReader
	Authz   Authorizer
	Now     func() time.Time
	NewID   func() string
}

// ItemView is an item with the keys of its project, organization and relations.
type ItemView struct {
	tracker.Item
	ProjectKey      string
	OrganizationKey string
	EpicKey         string
	MilestoneKey    string
}

// CreateItemInput is the input of CreateItem.
type CreateItemInput struct {
	ProjectKey         string
	Kind               tracker.Kind
	Title              string
	Description        string
	Type               tracker.TicketType // tickets; default feature
	AcceptanceCriteria []string           // tickets
	Policy             *tracker.Policy    // tickets; default fully autonomous
	EpicKey            string             // tickets
	MilestoneKey       string             // tickets and epics
}

// CreateItem creates a milestone, epic or ticket.
func (t *Tracker) CreateItem(ctx context.Context, in CreateItemInput) (ItemView, error) {
	id, p, c, err := t.authorizeProject(ctx, in.ProjectKey, ActTrackerWrite)
	if err != nil {
		return ItemView{}, err
	}
	now := t.Now()
	it := tracker.Item{
		ID: t.NewID(), ProjectID: p.ID, Kind: in.Kind, Title: in.Title, Description: in.Description,
		State: tracker.InitialState(in.Kind), CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if in.Kind == tracker.KindTicket {
		it.Type, it.AcceptanceCriteria, it.Policy = in.Type, in.AcceptanceCriteria, tracker.DefaultPolicy
		if it.Type == "" {
			it.Type = tracker.TypeFeature
		}
		if in.Policy != nil {
			it.Policy = *in.Policy
		}
	}
	view := ItemView{ProjectKey: p.Key, OrganizationKey: c.Key, EpicKey: in.EpicKey, MilestoneKey: in.MilestoneKey}
	if it.EpicID, err = t.relation(ctx, p, in.EpicKey, tracker.KindEpic, in.Kind == tracker.KindTicket); err != nil {
		return ItemView{}, err
	}
	if it.MilestoneID, err = t.relation(ctx, p, in.MilestoneKey, tracker.KindMilestone, in.Kind != tracker.KindMilestone); err != nil {
		return ItemView{}, err
	}
	if err := it.Validate(); err != nil {
		return ItemView{}, invalid(err)
	}
	e := itemEvent(it, c.ID, "item.created", actorOf(id), map[string]any{"kind": it.Kind, "title": it.Title})
	stored, err := t.Items.CreateItem(ctx, it, e)
	if err != nil {
		return ItemView{}, err
	}
	view.Item = stored
	return view, nil
}

// GetItem returns an item by key.
func (t *Tracker) GetItem(ctx context.Context, key string) (ItemView, error) {
	_, it, p, c, err := t.authorizeItem(ctx, key, ActTrackerRead)
	if err != nil {
		return ItemView{}, err
	}
	return t.view(ctx, it, p, c)
}

// ListItems returns a project's items matching the filter. EpicKey and
// MilestoneKey filter by relation.
func (t *Tracker) ListItems(ctx context.Context, projectKey string, kind tracker.Kind, state tracker.State, epicKey, milestoneKey string) ([]ItemView, error) {
	_, p, c, err := t.authorizeProject(ctx, projectKey, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	f := ItemFilter{Kind: kind, State: state}
	if f.EpicID, err = t.relation(ctx, p, epicKey, tracker.KindEpic, true); err != nil {
		return nil, err
	}
	if f.MilestoneID, err = t.relation(ctx, p, milestoneKey, tracker.KindMilestone, true); err != nil {
		return nil, err
	}
	items, err := t.Items.ListItems(ctx, p.ID, f)
	if err != nil {
		return nil, err
	}
	keys, err := t.projectKeys(ctx, items)
	if err != nil {
		return nil, err
	}
	out := make([]ItemView, 0, len(items))
	for _, it := range items {
		out = append(out, ItemView{
			Item: it, ProjectKey: p.Key, OrganizationKey: c.Key,
			EpicKey: keys[it.EpicID], MilestoneKey: keys[it.MilestoneID],
		})
	}
	return out, nil
}

// UpdateItemInput changes the non-nil fields of an item.
type UpdateItemInput struct {
	Key                string
	Version            int64
	Title              *string
	Description        *string
	Type               *tracker.TicketType
	AcceptanceCriteria *[]string
	Policy             *tracker.Policy
	EpicKey            *string // "" removes the epic
	MilestoneKey       *string // "" removes the milestone
}

// UpdateItem edits an item.
func (t *Tracker) UpdateItem(ctx context.Context, in UpdateItemInput) (ItemView, error) {
	id, it, p, c, err := t.authorizeItem(ctx, in.Key, ActTrackerWrite)
	if err != nil {
		return ItemView{}, err
	}
	changed := map[string]any{}
	if in.Title != nil {
		it.Title, changed["title"] = *in.Title, *in.Title
	}
	if in.Description != nil {
		it.Description, changed["description"] = *in.Description, true
	}
	if in.Type != nil {
		it.Type, changed["type"] = *in.Type, *in.Type
	}
	if in.AcceptanceCriteria != nil {
		it.AcceptanceCriteria, changed["acceptance_criteria"] = *in.AcceptanceCriteria, *in.AcceptanceCriteria
	}
	if in.Policy != nil {
		it.Policy, changed["policy"] = *in.Policy, *in.Policy
	}
	if in.EpicKey != nil {
		if it.EpicID, err = t.relation(ctx, p, *in.EpicKey, tracker.KindEpic, it.Kind == tracker.KindTicket); err != nil {
			return ItemView{}, err
		}
		changed["epic"] = *in.EpicKey
	}
	if in.MilestoneKey != nil {
		if it.MilestoneID, err = t.relation(ctx, p, *in.MilestoneKey, tracker.KindMilestone, it.Kind != tracker.KindMilestone); err != nil {
			return ItemView{}, err
		}
		changed["milestone"] = *in.MilestoneKey
	}
	if len(changed) == 0 {
		return ItemView{}, fmt.Errorf("%w: nothing to update", ErrInvalid)
	}
	if err := it.Validate(); err != nil {
		return ItemView{}, invalid(err)
	}
	it.UpdatedAt, it.Version = t.Now(), in.Version+1
	if err := t.Items.UpdateItem(ctx, it, in.Version, itemEvent(it, c.ID, "item.updated", actorOf(id), changed)); err != nil {
		return ItemView{}, err
	}
	return t.view(ctx, it, p, c)
}

// TransitionItem moves an item to another state on behalf of a human.
func (t *Tracker) TransitionItem(ctx context.Context, key string, to tracker.State, version int64) (ItemView, error) {
	id, it, p, c, err := t.authorizeItem(ctx, key, ActTrackerWrite)
	if err != nil {
		return ItemView{}, err
	}
	if !tracker.CanTransition(it.Kind, it.State, to) {
		return ItemView{}, fmt.Errorf("%w: cannot move %s %s from %s to %s", ErrInvalid, it.Kind, it.Key, it.State, to)
	}
	from := it.State
	it.State, it.UpdatedAt, it.Version = to, t.Now(), version+1
	if to != tracker.StateInProgress {
		it.Stage = ""
	}
	e := itemEvent(it, c.ID, "item.state_changed", actorOf(id), map[string]any{"from": from, "to": to})
	if err := t.Items.UpdateItem(ctx, it, version, e); err != nil {
		return ItemView{}, err
	}
	return t.view(ctx, it, p, c)
}

// ItemHistory returns the events of an item, oldest first.
func (t *Tracker) ItemHistory(ctx context.Context, key string) ([]event.Event, error) {
	_, it, _, _, err := t.authorizeItem(ctx, key, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	return t.Events.EntityHistory(ctx, "item", it.ID)
}

// authorizeProject loads a project and its organization and checks action on it.
func (t *Tracker) authorizeProject(ctx context.Context, projectKey string, a Action) (identity, tenancy.Project, tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	p, err := t.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	c, err := t.Tenancy.OrganizationByID(ctx, p.OrganizationID)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	if err := t.Authz.Authorize(ctx, id, a, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return identity{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	return id, p, c, nil
}

// authorizeItem loads an item with its project and organization and checks
// action on the project.
func (t *Tracker) authorizeItem(ctx context.Context, key string, a Action) (identity, tracker.Item, tenancy.Project, tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, tracker.Item{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	it, err := t.Items.ItemByKey(ctx, key)
	if err != nil {
		return identity{}, tracker.Item{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	p, c, err := t.projectOf(ctx, it.ProjectID)
	if err != nil {
		return identity{}, tracker.Item{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	if err := t.Authz.Authorize(ctx, id, a, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return identity{}, tracker.Item{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	return id, it, p, c, nil
}

// relation resolves the key of a related epic or milestone in project p.
// An empty key means no relation; allowed=false rejects any relation.
func (t *Tracker) relation(ctx context.Context, p tenancy.Project, key string, kind tracker.Kind, allowed bool) (string, error) {
	if key == "" {
		return "", nil
	}
	if !allowed {
		return "", fmt.Errorf("%w: this item cannot have a %s", ErrInvalid, kind)
	}
	rel, err := t.Items.ItemByKey(ctx, key)
	if err != nil {
		return "", fmt.Errorf("%w: %s %s does not exist", ErrInvalid, kind, key)
	}
	if rel.ProjectID != p.ID || rel.Kind != kind {
		return "", fmt.Errorf("%w: %s is not a %s of project %s", ErrInvalid, key, kind, p.Key)
	}
	return rel.ID, nil
}

func (t *Tracker) view(ctx context.Context, it tracker.Item, p tenancy.Project, c tenancy.Organization) (ItemView, error) {
	keys, err := t.projectKeys(ctx, []tracker.Item{it})
	if err != nil {
		return ItemView{}, err
	}
	return ItemView{Item: it, ProjectKey: p.Key, OrganizationKey: c.Key, EpicKey: keys[it.EpicID], MilestoneKey: keys[it.MilestoneID]}, nil
}

// projectKeys maps the IDs of epics and milestones referenced by items to
// their keys.
func (t *Tracker) projectKeys(ctx context.Context, items []tracker.Item) (map[string]string, error) {
	keys := map[string]string{"": ""}
	for _, it := range items {
		for _, ref := range []string{it.EpicID, it.MilestoneID} {
			if _, ok := keys[ref]; ok {
				continue
			}
			rel, err := t.Items.ItemByID(ctx, ref)
			if err != nil {
				return nil, err
			}
			keys[ref] = rel.Key
		}
	}
	return keys, nil
}

func (t *Tracker) projectOf(ctx context.Context, projectID string) (tenancy.Project, tenancy.Organization, error) {
	ps, err := t.Tenancy.ProjectByID(ctx, projectID)
	if err != nil {
		return tenancy.Project{}, tenancy.Organization{}, err
	}
	c, err := t.Tenancy.OrganizationByID(ctx, ps.OrganizationID)
	return ps, c, err
}

func itemEvent(it tracker.Item, organizationID, typ string, actor event.Actor, payload map[string]any) event.Event {
	return event.Event{
		Organization: organizationID, Project: it.ProjectID, EntityType: "item", EntityID: it.ID, Type: typ,
		Actor: actor, OccurredAt: it.UpdatedAt, Payload: mustJSON(payload),
	}
}
