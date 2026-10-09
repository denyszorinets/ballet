package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/auth"
)

// ChangesetStore persists plan changesets and applies them atomically.
type ChangesetStore interface {
	CreateChangeset(ctx context.Context, cs changeset.Changeset, e event.Event) error
	Changeset(ctx context.Context, id string) (changeset.Changeset, error)
	ListChangesets(ctx context.Context, projectID string, status changeset.Status) ([]changeset.Changeset, error)
	// DecideChangeset stores a decided changeset (applied or rejected) if
	// it is still proposed at expectedVersion, together with a.
	DecideChangeset(ctx context.Context, cs changeset.Changeset, expectedVersion int64, a ChangesetApplication, e event.Event) error
	NextItemNumber(ctx context.Context, projectID string) (int64, error)
}

// ChangesetApplication is everything applying the approved operations of
// a changeset writes. The store applies it in one atomic batch, guarded by
// the project's item sequence (NextItemNumber), its graph version (when
// dependencies are added) and the versions of updated items; a concurrent
// change yields ErrConflict and the application is rebuilt.
type ChangesetApplication struct {
	ProjectID      string
	NextItemNumber int64 // the sequence value the created items' keys assume
	GraphVersion   int64 // guarded when Dependencies is not empty
	Creates        []ItemWrite
	Updates        []ItemWrite
	Dependencies   []DependencyWrite
}

// ItemWrite is a created or updated item with its event.
type ItemWrite struct {
	Item            tracker.Item
	ExpectedVersion int64 // updates
	Event           event.Event
}

// DependencyWrite is an added dependency with its event.
type DependencyWrite struct {
	Dependency tracker.Dependency
	Event      event.Event
}

// Changesets implements plan changeset use cases. Proposing needs
// tracker.write on the project; applying additionally needs a human
// caller: the planner proposes, only humans approve.
type Changesets struct {
	Store   ChangesetStore
	Tracker *Tracker
}

// ChangesetView is a changeset with its project's key.
type ChangesetView struct {
	changeset.Changeset
	ProjectKey string
}

// ProposeInput is the input of Propose.
type ProposeInput struct {
	ProjectKey string
	Title      string
	Summary    string
	Ops        []changeset.Op
}

// Propose validates a changeset against the project as if every operation
// were approved, and stores it for a human to decide.
func (cs *Changesets) Propose(ctx context.Context, in ProposeInput) (ChangesetView, error) {
	id, p, c, err := cs.Tracker.authorizeProject(ctx, in.ProjectKey, ActTrackerWrite)
	if err != nil {
		return ChangesetView{}, err
	}
	return cs.propose(ctx, p, c, in, actorIn(ctx, id))
}

// proposeAs proposes on behalf of a workload (an agent run proposing
// work); the caller has checked that the actor may.
func (cs *Changesets) proposeAs(ctx context.Context, p tenancy.Project, c tenancy.Organization, in ProposeInput, actor event.Actor) (ChangesetView, error) {
	return cs.propose(ctx, p, c, in, actor)
}

func (cs *Changesets) propose(ctx context.Context, p tenancy.Project, c tenancy.Organization, in ProposeInput, actor event.Actor) (ChangesetView, error) {
	now := cs.Tracker.Now()
	ch := changeset.Changeset{
		ID: cs.Tracker.NewID(), ProjectID: p.ID, Title: in.Title, Summary: in.Summary, Ops: in.Ops,
		Status: changeset.StatusProposed, ProposedBy: actor, CreatedAt: now, Version: 1,
	}
	if err := ch.Validate(); err != nil {
		return ChangesetView{}, invalid(err)
	}
	all := make([]int, len(ch.Ops))
	for i := range all {
		all[i] = i
	}
	if _, _, err := cs.build(ctx, ch, all, p, c, auth.Identity{Kind: auth.KindService, Subject: actor.Subject}); err != nil {
		return ChangesetView{}, err
	}
	e := changesetEvent(ch, c.ID, "changeset.proposed", ch.ProposedBy, map[string]any{"title": ch.Title, "operations": len(ch.Ops)})
	if err := cs.Store.CreateChangeset(ctx, ch, e); err != nil {
		return ChangesetView{}, err
	}
	return ChangesetView{Changeset: ch, ProjectKey: p.Key}, nil
}

// Get returns a changeset.
func (cs *Changesets) Get(ctx context.Context, changesetID string) (ChangesetView, error) {
	ch, p, _, _, err := cs.load(ctx, changesetID, ActTrackerRead)
	if err != nil {
		return ChangesetView{}, err
	}
	return ChangesetView{Changeset: ch, ProjectKey: p.Key}, nil
}

// List returns a project's changesets, newest first; an empty status
// returns all.
func (cs *Changesets) List(ctx context.Context, projectKey string, status changeset.Status) ([]ChangesetView, error) {
	_, p, _, err := cs.Tracker.authorizeProject(ctx, projectKey, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	list, err := cs.Store.ListChangesets(ctx, p.ID, status)
	if err != nil {
		return nil, err
	}
	out := make([]ChangesetView, 0, len(list))
	for _, ch := range list {
		out = append(out, ChangesetView{Changeset: ch, ProjectKey: p.Key})
	}
	return out, nil
}

// Apply applies the approved operations (0-based indices) of a proposed
// changeset atomically and marks it applied. Operations are validated
// again against the current project.
func (cs *Changesets) Apply(ctx context.Context, changesetID string, approved []int) (ChangesetView, error) {
	ch, p, c, id, err := cs.load(ctx, changesetID, ActTrackerWrite)
	if err != nil {
		return ChangesetView{}, err
	}
	if _, planner := PlannerSessionOf(ctx); planner || id.Kind != auth.KindHuman {
		return ChangesetView{}, fmt.Errorf("%w: only a human can approve a changeset", ErrForbidden)
	}
	approved = slices.Clone(approved)
	slices.Sort(approved)
	for range graphRetries {
		if ch.Status != changeset.StatusProposed {
			return ChangesetView{}, fmt.Errorf("%w: changeset is already %s", ErrConflict, ch.Status)
		}
		if err := ch.CheckApproval(approved); err != nil {
			return ChangesetView{}, invalid(err)
		}
		a, results, err := cs.build(ctx, ch, approved, p, c, id)
		if err != nil {
			return ChangesetView{}, err
		}
		decided := ch
		decided.Status, decided.Approved, decided.Results = changeset.StatusApplied, approved, results
		decided.DecidedBy, decided.DecidedAt, decided.Version = actorOf(id), cs.Tracker.Now(), ch.Version+1
		e := changesetEvent(decided, c.ID, "changeset.applied", actorOf(id), map[string]any{"approved": len(approved)})
		err = cs.Store.DecideChangeset(ctx, decided, ch.Version, a, e)
		if errors.Is(err, ErrConflict) {
			// The project or the changeset changed meanwhile: reload and
			// validate again.
			if ch, err = cs.Store.Changeset(ctx, changesetID); err != nil {
				return ChangesetView{}, err
			}
			continue
		}
		if err != nil {
			return ChangesetView{}, err
		}
		return ChangesetView{Changeset: decided, ProjectKey: p.Key}, nil
	}
	return ChangesetView{}, fmt.Errorf("apply changeset: %w", ErrConflict)
}

// Reject marks a proposed changeset rejected; nothing is applied.
func (cs *Changesets) Reject(ctx context.Context, changesetID string) (ChangesetView, error) {
	ch, p, c, id, err := cs.load(ctx, changesetID, ActTrackerWrite)
	if err != nil {
		return ChangesetView{}, err
	}
	if _, planner := PlannerSessionOf(ctx); planner || id.Kind != auth.KindHuman {
		return ChangesetView{}, fmt.Errorf("%w: only a human can decide a changeset", ErrForbidden)
	}
	if ch.Status != changeset.StatusProposed {
		return ChangesetView{}, fmt.Errorf("%w: changeset is already %s", ErrConflict, ch.Status)
	}
	decided := ch
	decided.Status, decided.DecidedBy, decided.DecidedAt, decided.Version = changeset.StatusRejected, actorOf(id), cs.Tracker.Now(), ch.Version+1
	e := changesetEvent(decided, c.ID, "changeset.rejected", actorOf(id), nil)
	if err := cs.Store.DecideChangeset(ctx, decided, ch.Version, ChangesetApplication{ProjectID: p.ID}, e); err != nil {
		return ChangesetView{}, err
	}
	return ChangesetView{Changeset: decided, ProjectKey: p.Key}, nil
}

func (cs *Changesets) load(ctx context.Context, changesetID string, a Action) (changeset.Changeset, tenancy.Project, tenancy.Organization, identity, error) {
	id, err := caller(ctx)
	if err != nil {
		return changeset.Changeset{}, tenancy.Project{}, tenancy.Organization{}, identity{}, err
	}
	ch, err := cs.Store.Changeset(ctx, changesetID)
	if err != nil {
		return changeset.Changeset{}, tenancy.Project{}, tenancy.Organization{}, identity{}, err
	}
	p, c, err := cs.Tracker.projectOf(ctx, ch.ProjectID)
	if err != nil {
		return changeset.Changeset{}, tenancy.Project{}, tenancy.Organization{}, identity{}, err
	}
	if err := cs.Tracker.Authz.Authorize(ctx, id, a, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return changeset.Changeset{}, tenancy.Project{}, tenancy.Organization{}, identity{}, err
	}
	return ch, p, c, id, nil
}

// build turns the approved operations into the writes that apply them,
// validating each against the project's current items and dependency
// graph, and predicts the results (created keys follow the project's item
// sequence, which the store guards).
func (cs *Changesets) build(ctx context.Context, ch changeset.Changeset, approved []int, p tenancy.Project, c tenancy.Organization, id identity) (ChangesetApplication, []changeset.Result, error) {
	t := cs.Tracker
	next, err := cs.Store.NextItemNumber(ctx, p.ID)
	if err != nil {
		return ChangesetApplication{}, nil, err
	}
	graph, graphVersion, err := t.Deps.ProjectGraph(ctx, p.ID)
	if err != nil {
		return ChangesetApplication{}, nil, err
	}
	a := ChangesetApplication{ProjectID: p.ID, NextItemNumber: next, GraphVersion: graphVersion}
	results := make([]changeset.Result, len(ch.Ops))
	created := map[string]tracker.Item{} // by ref
	updated := map[string]bool{}         // item IDs
	actor := actorOf(id)
	now := t.Now()

	resolve := func(ref string) (tracker.Item, error) {
		if r, ok := changeset.IsRef(ref); ok {
			it, ok := created[r]
			if !ok {
				return tracker.Item{}, fmt.Errorf("%s is not approved", ref)
			}
			return it, nil
		}
		it, err := t.Items.ItemByKey(ctx, ref)
		if errors.Is(err, ErrNotFound) {
			return tracker.Item{}, fmt.Errorf("%s does not exist", ref)
		}
		if err != nil {
			return tracker.Item{}, storeError{err}
		}
		if it.ProjectID != p.ID {
			return tracker.Item{}, fmt.Errorf("%s is not in project %s", ref, p.Key)
		}
		return it, nil
	}
	relation := func(ref string, kind tracker.Kind, allowed bool) (string, error) {
		if ref == "" {
			return "", nil
		}
		if !allowed {
			return "", fmt.Errorf("this item cannot have a %s", kind)
		}
		rel, err := resolve(ref)
		if err != nil {
			return "", err
		}
		if rel.Kind != kind {
			return "", fmt.Errorf("%s is a %s, not a %s", ref, rel.Kind, kind)
		}
		return rel.ID, nil
	}

	for _, i := range approved {
		op := ch.Ops[i]
		fail := func(err error) (ChangesetApplication, []changeset.Result, error) {
			var se storeError
			if errors.As(err, &se) {
				return ChangesetApplication{}, nil, se.error
			}
			return ChangesetApplication{}, nil, fmt.Errorf("%w: operation %d: %w", ErrInvalid, i+1, err)
		}
		switch op.Kind {
		case changeset.OpCreateItem:
			in := op.Create
			it := tracker.Item{
				ID: t.NewID(), ProjectID: p.ID, Number: next, Key: p.Key + "-" + strconv.FormatInt(next, 10),
				Kind: in.Kind, Title: in.Title, Description: in.Description, State: tracker.InitialState(in.Kind),
				CreatedAt: now, UpdatedAt: now, Version: 1,
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
			if it.EpicID, err = relation(in.Epic, tracker.KindEpic, in.Kind == tracker.KindTicket); err != nil {
				return fail(err)
			}
			if it.MilestoneID, err = relation(in.Milestone, tracker.KindMilestone, in.Kind != tracker.KindMilestone); err != nil {
				return fail(err)
			}
			if err := it.Validate(); err != nil {
				return fail(err)
			}
			next++
			created[op.Ref] = it
			results[i].Key = it.Key
			a.Creates = append(a.Creates, ItemWrite{Item: it, Event: itemEvent(it, c.ID, "item.created", actor,
				map[string]any{"kind": it.Kind, "title": it.Title, "changeset": ch.ID})})

		case changeset.OpUpdateItem:
			in := op.Update
			it, err := resolve(in.Item)
			if err != nil {
				return fail(err)
			}
			if updated[it.ID] {
				return fail(fmt.Errorf("%s is updated twice", it.Key))
			}
			updated[it.ID] = true
			expected := it.Version
			changed := map[string]any{"changeset": ch.ID}
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
			if in.Epic != nil {
				if it.EpicID, err = relation(*in.Epic, tracker.KindEpic, it.Kind == tracker.KindTicket); err != nil {
					return fail(err)
				}
				changed["epic"] = *in.Epic
			}
			if in.Milestone != nil {
				if it.MilestoneID, err = relation(*in.Milestone, tracker.KindMilestone, it.Kind != tracker.KindMilestone); err != nil {
					return fail(err)
				}
				changed["milestone"] = *in.Milestone
			}
			if err := it.Validate(); err != nil {
				return fail(err)
			}
			it.UpdatedAt, it.Version = now, expected+1
			results[i].Key = it.Key
			a.Updates = append(a.Updates, ItemWrite{Item: it, ExpectedVersion: expected,
				Event: itemEvent(it, c.ID, "item.updated", actor, changed)})

		case changeset.OpAddDependency:
			in := op.Dependency
			from, err := resolve(in.From)
			if err != nil {
				return fail(err)
			}
			to, err := resolve(in.To)
			if err != nil {
				return fail(err)
			}
			d := tracker.NormalizeRelates(tracker.Dependency{
				ID: t.NewID(), ProjectID: p.ID, FromID: from.ID, ToID: to.ID, Type: in.Type, CreatedAt: now,
			})
			for _, e := range graph {
				if e.FromID == d.FromID && e.ToID == d.ToID && e.Type == d.Type {
					return fail(fmt.Errorf("%s already %s %s", from.Key, d.Type, to.Key))
				}
			}
			if err := tracker.ValidateDependency(graph, d); err != nil {
				return fail(err)
			}
			graph = append(graph, d)
			results[i].DependencyID = d.ID
			keys := map[string]string{from.ID: from.Key, to.ID: to.Key}
			a.Dependencies = append(a.Dependencies, DependencyWrite{Dependency: d, Event: event.Event{
				Organization: c.ID, Project: p.ID, EntityType: "dependency", EntityID: d.ID, Type: "dependency.added",
				Actor: actor, OccurredAt: now,
				Payload: mustJSON(map[string]any{"from": keys[d.FromID], "to": keys[d.ToID], "type": d.Type, "changeset": ch.ID}),
			}})
		}
	}
	return a, results, nil
}

// storeError marks a failure to read the project while building an
// application, as opposed to an invalid operation.
type storeError struct{ error }

func changesetEvent(ch changeset.Changeset, organizationID, typ string, actor event.Actor, payload map[string]any) event.Event {
	if payload == nil {
		payload = map[string]any{}
	}
	at := ch.CreatedAt
	if !ch.DecidedAt.IsZero() {
		at = ch.DecidedAt
	}
	return event.Event{
		Organization: organizationID, Project: ch.ProjectID, EntityType: "changeset", EntityID: ch.ID, Type: typ,
		Actor: actor, OccurredAt: at, Payload: mustJSON(payload),
	}
}
