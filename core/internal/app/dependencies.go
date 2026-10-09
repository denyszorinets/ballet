package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// DependencyStore persists dependency edges.
type DependencyStore interface {
	ProjectGraph(ctx context.Context, projectID string) ([]tracker.Dependency, int64, error)
	ItemDependencies(ctx context.Context, itemID string) ([]tracker.Dependency, error)
	Dependency(ctx context.Context, id string) (tracker.Dependency, error)
	AddDependency(ctx context.Context, d tracker.Dependency, graphVersion int64, e event.Event) error
	RemoveDependency(ctx context.Context, d tracker.Dependency, e event.Event) error
	ListRunnable(ctx context.Context, projectID string) ([]tracker.Item, error)
}

// Direction describes an edge from the point of view of one item.
type Direction string

// Directions.
const (
	DirBlocks    Direction = "blocks"     // this item blocks the other
	DirBlockedBy Direction = "blocked_by" // the other item blocks this one
	DirRelates   Direction = "relates"
)

// DependencyView is an edge seen from one item.
type DependencyView struct {
	ID        string
	Direction Direction
	Other     tracker.Item
}

// graphRetries bounds retries when concurrent graph changes conflict.
const graphRetries = 5

// AddDependency links item key to item other. Direction is from key's point
// of view: blocks (key blocks other), blocked_by (other blocks key) or
// relates. Blocking cycles are rejected.
func (t *Tracker) AddDependency(ctx context.Context, key string, dir Direction, other string) (DependencyView, error) {
	id, it, p, c, err := t.authorizeItem(ctx, key, ActTrackerWrite)
	if err != nil {
		return DependencyView{}, err
	}
	o, err := t.Items.ItemByKey(ctx, other)
	if err != nil {
		return DependencyView{}, err
	}
	if o.ProjectID != it.ProjectID {
		return DependencyView{}, fmt.Errorf("%w: %s and %s are in different projects", ErrInvalid, key, other)
	}
	d := tracker.Dependency{ID: t.NewID(), ProjectID: p.ID, CreatedAt: t.Now()}
	switch dir {
	case DirBlocks:
		d.FromID, d.ToID, d.Type = it.ID, o.ID, tracker.DepBlocks
	case DirBlockedBy:
		d.FromID, d.ToID, d.Type = o.ID, it.ID, tracker.DepBlocks
	case DirRelates:
		d = tracker.NormalizeRelates(tracker.Dependency{
			ID: d.ID, ProjectID: d.ProjectID, CreatedAt: d.CreatedAt, FromID: it.ID, ToID: o.ID, Type: tracker.DepRelates,
		})
	default:
		return DependencyView{}, fmt.Errorf("%w: type must be blocks, blocked_by or relates", ErrInvalid)
	}
	keys := map[string]string{it.ID: it.Key, o.ID: o.Key}
	e := event.Event{
		Organization: c.ID, Project: p.ID, EntityType: "dependency", EntityID: d.ID, Type: "dependency.added",
		Actor: actorOf(id), OccurredAt: d.CreatedAt,
		Payload: mustJSON(map[string]any{"from": keys[d.FromID], "to": keys[d.ToID], "type": d.Type}),
	}

	for range graphRetries {
		graph, version, err := t.Deps.ProjectGraph(ctx, p.ID)
		if err != nil {
			return DependencyView{}, err
		}
		if err := tracker.ValidateDependency(graph, d); err != nil {
			return DependencyView{}, invalid(err)
		}
		err = t.Deps.AddDependency(ctx, d, version, e)
		if errors.Is(err, ErrConflict) {
			continue // the graph changed meanwhile: validate again
		}
		if err != nil {
			return DependencyView{}, err
		}
		return DependencyView{ID: d.ID, Direction: dir, Other: o}, nil
	}
	return DependencyView{}, fmt.Errorf("add dependency: %w", ErrConflict)
}

// RemoveDependency deletes an edge.
func (t *Tracker) RemoveDependency(ctx context.Context, depID string) error {
	id, err := caller(ctx)
	if err != nil {
		return err
	}
	d, err := t.Deps.Dependency(ctx, depID)
	if err != nil {
		return err
	}
	p, c, err := t.projectOf(ctx, d.ProjectID)
	if err != nil {
		return err
	}
	if err := t.Authz.Authorize(ctx, id, ActTrackerWrite, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return err
	}
	from, err := t.Items.ItemByID(ctx, d.FromID)
	if err != nil {
		return err
	}
	to, err := t.Items.ItemByID(ctx, d.ToID)
	if err != nil {
		return err
	}
	e := event.Event{
		Organization: c.ID, Project: p.ID, EntityType: "dependency", EntityID: d.ID, Type: "dependency.removed",
		Actor: actorOf(id), OccurredAt: t.Now(),
		Payload: mustJSON(map[string]any{"from": from.Key, "to": to.Key, "type": d.Type}),
	}
	return t.Deps.RemoveDependency(ctx, d, e)
}

// Dependencies returns the edges of an item from its point of view.
func (t *Tracker) Dependencies(ctx context.Context, key string) ([]DependencyView, error) {
	_, it, _, _, err := t.authorizeItem(ctx, key, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	deps, err := t.Deps.ItemDependencies(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	out := make([]DependencyView, 0, len(deps))
	for _, d := range deps {
		v := DependencyView{ID: d.ID}
		otherID := d.ToID
		switch {
		case d.Type == tracker.DepRelates:
			v.Direction = DirRelates
			if d.ToID == it.ID {
				otherID = d.FromID
			}
		case d.FromID == it.ID:
			v.Direction = DirBlocks
		default:
			v.Direction, otherID = DirBlockedBy, d.FromID
		}
		if v.Other, err = t.Items.ItemByID(ctx, otherID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Runnable returns the project's tickets that are ready and unblocked.
func (t *Tracker) Runnable(ctx context.Context, projectKey string) ([]ItemView, error) {
	_, p, c, err := t.authorizeProject(ctx, projectKey, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	items, err := t.Deps.ListRunnable(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	keys, err := t.projectKeys(ctx, items)
	if err != nil {
		return nil, err
	}
	out := make([]ItemView, 0, len(items))
	for _, it := range items {
		out = append(out, ItemView{Item: it, ProjectKey: p.Key, OrganizationKey: c.Key, EpicKey: keys[it.EpicID], MilestoneKey: keys[it.MilestoneID]})
	}
	return out, nil
}
