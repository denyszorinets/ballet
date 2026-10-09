package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/auth"
)

// FeatureStore persists the feature map (ADR-0028).
type FeatureStore interface {
	NextFeatureNumber(ctx context.Context, organizationID string) (int64, error)
	// CreateFeatures stores new features with their first revisions,
	// guarded by the organization's feature sequence (next); a concurrent
	// creation yields ErrConflict.
	CreateFeatures(ctx context.Context, organizationID string, next int64, ws []FeatureWrite) error
	// UpdateFeature stores w if the feature is still at expected.
	UpdateFeature(ctx context.Context, w FeatureWrite, expected int64) error
	FeatureByKey(ctx context.Context, organizationID, key string) (feature.Feature, error)
	ListFeatures(ctx context.Context, organizationID string) ([]feature.Feature, error)
	FeatureRevisions(ctx context.Context, featureID string) ([]feature.Revision, error)
	// OrganizationRevisions returns every revision of the organization's
	// features without descriptions, oldest first.
	OrganizationRevisions(ctx context.Context, organizationID string) ([]feature.Revision, error)
	// FeatureLinks returns the organization's links, removed ones included.
	FeatureLinks(ctx context.Context, organizationID string) ([]feature.Link, error)
	AddFeatureLink(ctx context.Context, l feature.Link, e event.Event) error
	// RemoveFeatureLink marks a valid link removed.
	RemoveFeatureLink(ctx context.Context, l feature.Link, e event.Event) error
	// PendingRevisions returns the revisions of an organization's
	// features that wait for review, oldest first.
	PendingRevisions(ctx context.Context, organizationID string) ([]feature.Revision, error)
	// ReviewRevision records the review of a pending revision; ErrConflict
	// when it is no longer pending.
	ReviewRevision(ctx context.Context, m ReviewMark, e event.Event) error

	// SetTicketFeatures replaces the features a ticket changes.
	SetTicketFeatures(ctx context.Context, itemID string, featureIDs []string, at time.Time, e event.Event) error
	TicketFeatures(ctx context.Context, itemID string) ([]feature.Feature, error)
	FeatureTickets(ctx context.Context, featureID string) ([]tracker.Item, error)
	// DeliveryStates returns the ticket states of features whose status
	// follows their tickets.
	DeliveryStates(ctx context.Context) ([]FeatureTicketState, error)
	FeatureByID(ctx context.Context, id string) (feature.Feature, error)
}

// FeatureTicketState is the state of one ticket changing a feature.
type FeatureTicketState struct {
	FeatureID string
	TicketKey string
	State     tracker.State
}

// FeatureWrite is a feature's new state, its revision and its event.
type FeatureWrite struct {
	Feature  feature.Feature
	Revision feature.Revision
	Event    event.Event
	// Reviews marks a pending revision reviewed in the same write (a
	// revert); nil: none.
	Reviews *ReviewMark
}

// ReviewMark is the review of a pending revision.
type ReviewMark struct {
	FeatureID string
	Number    int64
	Review    feature.Review
	By        string
	At        time.Time
}

// Features implements the feature map's use cases. Reading needs
// tracker.read on the organization, or on a project, which then shows the
// features of the projects it can read. Writing needs tracker.write on the
// organization, or on every project of the feature.
type Features struct {
	Store     FeatureStore
	Tenancy   TenancyStore
	Execution ExecutionStore // projects' policy overrides; nil: the organization's policy
	Items     ItemStore      // ticket links; nil: none
	Authz     Authorizer
	Now       func() time.Time
	NewID     func() string
}

// FeatureView is a feature with its organization's and projects' keys.
type FeatureView struct {
	feature.Feature
	OrganizationKey string
	ProjectKeys     []string
}

// FeatureLinkView is a link with its features' keys.
type FeatureLinkView struct {
	feature.Link
	FromKey string
	ToKey   string
}

// FeatureDetail is a feature with its current links and the tickets
// that change it.
type FeatureDetail struct {
	FeatureView
	Links   []FeatureLinkView
	Tickets []FeatureTicket
}

// FeatureTicket is a ticket changing a feature.
type FeatureTicket struct {
	Key        string
	Title      string
	State      tracker.State
	ProjectKey string
}

// RevisionView is a revision with its projects' keys.
type RevisionView struct {
	feature.Revision
	FeatureKey  string
	ProjectKeys []string
}

// FeatureFilter selects features. Zero fields do not filter.
type FeatureFilter struct {
	ProjectKey string
	Status     feature.Status
}

// featureScope is what a caller can see and do in an organization.
type featureScope struct {
	id       identity
	org      tenancy.Organization
	projects map[string]tenancy.Project // by ID: the organization's projects
	all      bool                       // reads the whole organization
	readable map[string]bool            // project IDs readable when not all
}

func (fs *Features) scope(ctx context.Context, orgKey string) (featureScope, error) {
	id, err := caller(ctx)
	if err != nil {
		return featureScope{}, err
	}
	org, err := fs.Tenancy.OrganizationByKey(ctx, orgKey)
	if errors.Is(err, ErrNotFound) {
		return featureScope{}, ErrForbidden
	}
	if err != nil {
		return featureScope{}, err
	}
	ps, err := fs.Tenancy.ListProjects(ctx, org.ID)
	if err != nil {
		return featureScope{}, err
	}
	s := featureScope{id: id, org: org, projects: map[string]tenancy.Project{}, readable: map[string]bool{}}
	for _, p := range ps {
		s.projects[p.ID] = p
	}
	if fs.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Organization: org.Key}) == nil {
		s.all = true
		return s, nil
	}
	for _, p := range ps {
		if fs.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Organization: org.Key, Project: p.Key}) == nil {
			s.readable[p.ID] = true
		}
	}
	if len(s.readable) == 0 {
		return featureScope{}, ErrForbidden
	}
	return s, nil
}

// sees reports whether the caller may read a feature in projects.
func (s featureScope) sees(projectIDs []string) bool {
	return s.all || slices.ContainsFunc(projectIDs, func(p string) bool { return s.readable[p] })
}

// canWrite checks tracker.write on the organization or on every project.
func (fs *Features) canWrite(ctx context.Context, s featureScope, projectIDs []string) error {
	if fs.Authz.Authorize(ctx, s.id, ActTrackerWrite, Scope{Organization: s.org.Key}) == nil {
		return nil
	}
	if len(projectIDs) == 0 {
		return ErrForbidden
	}
	for _, p := range projectIDs {
		if err := fs.Authz.Authorize(ctx, s.id, ActTrackerWrite, Scope{Organization: s.org.Key, Project: s.projects[p].Key}); err != nil {
			return err
		}
	}
	return nil
}

func (s featureScope) projectIDs(keys []string) ([]string, error) {
	byKey := map[string]string{}
	for id, p := range s.projects {
		byKey[p.Key] = id
	}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		id, ok := byKey[k]
		if !ok {
			return nil, fmt.Errorf("%w: project %s is not in organization %s", ErrInvalid, k, s.org.Key)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s featureScope) projectKeys(ids []string) []string {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		if p, ok := s.projects[id]; ok {
			keys = append(keys, p.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

func (s featureScope) view(f feature.Feature) FeatureView {
	return FeatureView{Feature: f, OrganizationKey: s.org.Key, ProjectKeys: s.projectKeys(f.ProjectIDs)}
}

// load returns a feature the caller can see.
func (fs *Features) load(ctx context.Context, s featureScope, key string) (feature.Feature, error) {
	if _, err := feature.ParseKey(key); err != nil {
		return feature.Feature{}, invalid(err)
	}
	f, err := fs.Store.FeatureByKey(ctx, s.org.ID, key)
	if err != nil {
		return feature.Feature{}, err
	}
	if !s.sees(f.ProjectIDs) {
		return feature.Feature{}, fmt.Errorf("feature %s: %w", key, ErrNotFound)
	}
	return f, nil
}

// List returns the organization's features the caller can see.
func (fs *Features) List(ctx context.Context, orgKey string, f FeatureFilter) ([]FeatureView, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return nil, err
	}
	all, err := fs.Store.ListFeatures(ctx, s.org.ID)
	if err != nil {
		return nil, err
	}
	var project string
	if f.ProjectKey != "" {
		ids, err := s.projectIDs([]string{f.ProjectKey})
		if err != nil {
			return nil, err
		}
		project = ids[0]
	}
	out := []FeatureView{}
	for _, ft := range all {
		if !s.sees(ft.ProjectIDs) || (f.Status != "" && ft.Status != f.Status) ||
			(project != "" && !slices.Contains(ft.ProjectIDs, project)) {
			continue
		}
		out = append(out, s.view(ft))
	}
	return out, nil
}

// Get returns a feature with its current links to features the caller can
// see.
func (fs *Features) Get(ctx context.Context, orgKey, key string) (FeatureDetail, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return FeatureDetail{}, err
	}
	f, err := fs.load(ctx, s, key)
	if err != nil {
		return FeatureDetail{}, err
	}
	links, err := fs.linkViews(ctx, s, fs.Now(), func(l feature.Link) bool { return l.FromID == f.ID || l.ToID == f.ID })
	if err != nil {
		return FeatureDetail{}, err
	}
	items, err := fs.Store.FeatureTickets(ctx, f.ID)
	if err != nil {
		return FeatureDetail{}, err
	}
	tickets := []FeatureTicket{}
	for _, it := range items {
		if s.all || s.readable[it.ProjectID] {
			tickets = append(tickets, FeatureTicket{Key: it.Key, Title: it.Title, State: it.State, ProjectKey: s.projects[it.ProjectID].Key})
		}
	}
	return FeatureDetail{FeatureView: s.view(f), Links: links, Tickets: tickets}, nil
}

// Revisions returns a feature's revisions, newest first.
func (fs *Features) Revisions(ctx context.Context, orgKey, key string) ([]RevisionView, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return nil, err
	}
	f, err := fs.load(ctx, s, key)
	if err != nil {
		return nil, err
	}
	revs, err := fs.Store.FeatureRevisions(ctx, f.ID)
	if err != nil {
		return nil, err
	}
	out := make([]RevisionView, len(revs))
	for i, r := range revs {
		out[i] = RevisionView{Revision: r, FeatureKey: f.Key, ProjectKeys: s.projectKeys(r.ProjectIDs)}
	}
	return out, nil
}

// CreateFeatureInput is the input of Create.
type CreateFeatureInput struct {
	Title       string
	Description string
	Status      feature.Status // default planned
	ProjectKeys []string
	Reason      string
}

// Create adds a feature to the organization's map.
func (fs *Features) Create(ctx context.Context, orgKey string, in CreateFeatureInput) (FeatureView, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return FeatureView{}, err
	}
	pids, err := s.projectIDs(in.ProjectKeys)
	if err != nil {
		return FeatureView{}, err
	}
	if err := fs.canWrite(ctx, s, pids); err != nil {
		return FeatureView{}, err
	}
	if err := feature.ValidateReason(in.Reason); err != nil {
		return FeatureView{}, invalid(err)
	}
	status := in.Status
	if status == "" {
		status = feature.StatusPlanned
	}
	actor := actorIn(ctx, s.id)
	for range graphRetries {
		next, err := fs.Store.NextFeatureNumber(ctx, s.org.ID)
		if err != nil {
			return FeatureView{}, err
		}
		now := fs.Now()
		f := feature.Feature{
			ID: fs.NewID(), OrganizationID: s.org.ID, Number: next, Key: feature.Key(next),
			Title: in.Title, Description: in.Description, Status: status, ProjectIDs: pids,
			CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		if err := f.Validate(); err != nil {
			return FeatureView{}, invalid(err)
		}
		w := fs.write(f, actor, in.Reason, feature.Cause{}, "feature.created", map[string]any{"title": f.Title})
		err = fs.Store.CreateFeatures(ctx, s.org.ID, next, []FeatureWrite{w})
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return FeatureView{}, err
		}
		return s.view(f), nil
	}
	return FeatureView{}, fmt.Errorf("create feature: %w", ErrConflict)
}

// UpdateFeatureInput changes the non-nil fields of a feature.
type UpdateFeatureInput struct {
	Version     int64
	Title       *string
	Description *string
	Status      *feature.Status
	ProjectKeys *[]string
	Reason      string
}

// Update changes a feature, appending a revision.
func (fs *Features) Update(ctx context.Context, orgKey, key string, in UpdateFeatureInput) (FeatureView, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return FeatureView{}, err
	}
	f, err := fs.load(ctx, s, key)
	if err != nil {
		return FeatureView{}, err
	}
	if err := fs.canWrite(ctx, s, f.ProjectIDs); err != nil {
		return FeatureView{}, err
	}
	if f.Version != in.Version {
		return FeatureView{}, fmt.Errorf("feature %s is at version %d: %w", key, f.Version, ErrConflict)
	}
	next, changed, err := fs.apply(s, f, in)
	if err != nil {
		return FeatureView{}, err
	}
	if next.ProjectIDs != nil && !slices.Equal(next.ProjectIDs, f.ProjectIDs) {
		if err := fs.canWrite(ctx, s, next.ProjectIDs); err != nil {
			return FeatureView{}, err
		}
	}
	w := fs.write(next, actorIn(ctx, s.id), in.Reason, feature.Cause{}, "feature.updated", changed)
	if err := fs.Store.UpdateFeature(ctx, w, f.Version); err != nil {
		return FeatureView{}, err
	}
	return s.view(next), nil
}

// apply returns f changed by in, with the changed fields for the event.
func (fs *Features) apply(s featureScope, f feature.Feature, in UpdateFeatureInput) (feature.Feature, map[string]any, error) {
	next := f
	changed := map[string]any{}
	if in.Title != nil && *in.Title != f.Title {
		next.Title, changed["title"] = *in.Title, *in.Title
	}
	if in.Description != nil && *in.Description != f.Description {
		next.Description, changed["description"] = *in.Description, true
	}
	if in.Status != nil && *in.Status != f.Status {
		next.Status, changed["status"] = *in.Status, *in.Status
	}
	if in.ProjectKeys != nil {
		pids, err := s.projectIDs(*in.ProjectKeys)
		if err != nil {
			return feature.Feature{}, nil, err
		}
		if !sameSet(pids, f.ProjectIDs) {
			next.ProjectIDs, changed["projects"] = pids, *in.ProjectKeys
		}
	}
	if len(changed) == 0 {
		return feature.Feature{}, nil, invalid(errors.New("the update changes nothing"))
	}
	if err := errors.Join(next.Validate(), feature.ValidateReason(in.Reason)); err != nil {
		return feature.Feature{}, nil, invalid(err)
	}
	next.Version, next.UpdatedAt = f.Version+1, fs.Now()
	return next, changed, nil
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// write builds the revision and event recording f's new state. Revisions
// by agents and the planner (service actors) wait for review; those by
// humans and by Ballet itself do not.
func (fs *Features) write(f feature.Feature, actor event.Actor, reason string, cause feature.Cause, typ string, payload map[string]any) FeatureWrite {
	review := feature.ReviewNone
	if actor.Kind == event.ActorService {
		review = feature.ReviewPending
	}
	payload["key"], payload["version"] = f.Key, f.Version
	if reason != "" {
		payload["reason"] = reason
	}
	return FeatureWrite{
		Feature:  f,
		Revision: feature.RevisionOf(f, actor, reason, cause, review),
		Event: event.Event{
			Organization: f.OrganizationID, EntityType: "feature", EntityID: f.ID, Type: typ,
			Actor: actor, OccurredAt: f.UpdatedAt, Payload: mustJSON(payload),
		},
	}
}

// LinkFeaturesInput is the input of Link.
type LinkFeaturesInput struct {
	From string // feature key
	To   string // feature key
	Type feature.LinkType
}

// Link adds a link between two features.
func (fs *Features) Link(ctx context.Context, orgKey string, in LinkFeaturesInput) (FeatureLinkView, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return FeatureLinkView{}, err
	}
	resolve := func(key string) (feature.Feature, error) {
		f, err := fs.load(ctx, s, key)
		if errors.Is(err, ErrNotFound) {
			return feature.Feature{}, invalid(fmt.Errorf("feature %s does not exist", key))
		}
		return f, err
	}
	from, err := resolve(in.From)
	if err != nil {
		return FeatureLinkView{}, err
	}
	to, err := resolve(in.To)
	if err != nil {
		return FeatureLinkView{}, err
	}
	if err := fs.canWrite(ctx, s, from.ProjectIDs); err != nil {
		return FeatureLinkView{}, err
	}
	now := fs.Now()
	l := feature.Link{
		ID: fs.NewID(), OrganizationID: s.org.ID, FromID: from.ID, ToID: to.ID, Type: in.Type,
		CreatedBy: actorIn(ctx, s.id), CreatedAt: now,
	}
	if err := l.Validate(); err != nil {
		return FeatureLinkView{}, invalid(err)
	}
	existing, err := fs.Store.FeatureLinks(ctx, s.org.ID)
	if err != nil {
		return FeatureLinkView{}, err
	}
	for _, e := range existing {
		if e.RemovedAt.IsZero() && e.FromID == l.FromID && e.ToID == l.ToID && e.Type == l.Type {
			return FeatureLinkView{}, invalid(fmt.Errorf("%s already %s %s", from.Key, l.Type, to.Key))
		}
	}
	if err := fs.Store.AddFeatureLink(ctx, l, linkEvent(l, "feature.linked", l.CreatedBy, from.Key, to.Key)); err != nil {
		return FeatureLinkView{}, err
	}
	return FeatureLinkView{Link: l, FromKey: from.Key, ToKey: to.Key}, nil
}

// Unlink removes a link; it stays in the map's history.
func (fs *Features) Unlink(ctx context.Context, orgKey, linkID string) error {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return err
	}
	links, err := fs.Store.FeatureLinks(ctx, s.org.ID)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(links, func(l feature.Link) bool { return l.ID == linkID && l.RemovedAt.IsZero() })
	if i < 0 {
		return fmt.Errorf("feature link %s: %w", linkID, ErrNotFound)
	}
	l := links[i]
	features, err := fs.byID(ctx, s)
	if err != nil {
		return err
	}
	from, to := features[l.FromID], features[l.ToID]
	if !s.sees(from.ProjectIDs) || !s.sees(to.ProjectIDs) {
		return fmt.Errorf("feature link %s: %w", linkID, ErrNotFound)
	}
	if err := fs.canWrite(ctx, s, from.ProjectIDs); err != nil {
		return err
	}
	l.RemovedBy, l.RemovedAt = actorIn(ctx, s.id), fs.Now()
	return fs.Store.RemoveFeatureLink(ctx, l, linkEvent(l, "feature.unlinked", l.RemovedBy, from.Key, to.Key))
}

func linkEvent(l feature.Link, typ string, actor event.Actor, from, to string) event.Event {
	at := l.CreatedAt
	if !l.RemovedAt.IsZero() {
		at = l.RemovedAt
	}
	return event.Event{
		Organization: l.OrganizationID, EntityType: "feature_link", EntityID: l.ID, Type: typ, Actor: actor,
		OccurredAt: at, Payload: mustJSON(map[string]any{"from": from, "to": to, "type": l.Type}),
	}
}

func (fs *Features) byID(ctx context.Context, s featureScope) (map[string]feature.Feature, error) {
	all, err := fs.Store.ListFeatures(ctx, s.org.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]feature.Feature, len(all))
	for _, f := range all {
		out[f.ID] = f
	}
	return out, nil
}

// linkViews returns the links valid at t that match keep, between features
// the caller can see.
func (fs *Features) linkViews(ctx context.Context, s featureScope, t time.Time, keep func(feature.Link) bool) ([]FeatureLinkView, error) {
	links, err := fs.Store.FeatureLinks(ctx, s.org.ID)
	if err != nil {
		return nil, err
	}
	features, err := fs.byID(ctx, s)
	if err != nil {
		return nil, err
	}
	out := []FeatureLinkView{}
	for _, l := range links {
		from, to := features[l.FromID], features[l.ToID]
		if !l.ValidAt(t) || !keep(l) || !s.sees(from.ProjectIDs) || !s.sees(to.ProjectIDs) {
			continue
		}
		out = append(out, FeatureLinkView{Link: l, FromKey: from.Key, ToKey: to.Key})
	}
	return out, nil
}

// GraphQuery selects the map to return. Zero fields: now, all projects.
type GraphQuery struct {
	At         time.Time
	ProjectKey string
}

// GraphFeature is a feature as it was at the graph's time.
type GraphFeature struct {
	Key         string
	Title       string
	Status      feature.Status
	ProjectKeys []string
	Version     int64 // its latest revision at that time
	CreatedAt   time.Time
	UpdatedAt   time.Time // of that revision
}

// GraphChange is a moment the map changed: a revision or a link added or
// removed.
type GraphChange struct {
	At         time.Time
	FeatureKey string
	Kind       string // "revision", "linked" or "unlinked"
	Summary    string
}

// FeatureGraph is the organization's map at one time, and every change in
// its history (for a time slider).
type FeatureGraph struct {
	At       time.Time
	Features []GraphFeature
	Links    []FeatureLinkView
	Changes  []GraphChange
}

// Graph returns the feature map as it was at q.At: each feature in its
// latest revision at that time, and the links valid then.
func (fs *Features) Graph(ctx context.Context, orgKey string, q GraphQuery) (FeatureGraph, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return FeatureGraph{}, err
	}
	at := q.At
	if at.IsZero() {
		at = fs.Now()
	}
	var project string
	if q.ProjectKey != "" {
		ids, err := s.projectIDs([]string{q.ProjectKey})
		if err != nil {
			return FeatureGraph{}, err
		}
		project = ids[0]
	}
	features, err := fs.byID(ctx, s)
	if err != nil {
		return FeatureGraph{}, err
	}
	revs, err := fs.Store.OrganizationRevisions(ctx, s.org.ID)
	if err != nil {
		return FeatureGraph{}, err
	}
	g := FeatureGraph{At: at, Features: []GraphFeature{}, Links: []FeatureLinkView{}, Changes: []GraphChange{}}
	state := map[string]GraphFeature{} // by feature ID
	shown := map[string]bool{}
	for _, r := range revs { // oldest first
		f := features[r.FeatureID]
		if !s.sees(f.ProjectIDs) {
			continue
		}
		g.Changes = append(g.Changes, GraphChange{At: r.CreatedAt, FeatureKey: f.Key, Kind: "revision", Summary: revisionSummary(r)})
		if r.CreatedAt.After(at) {
			continue
		}
		gf := GraphFeature{Key: f.Key, Title: r.Title, Status: r.Status, ProjectKeys: s.projectKeys(r.ProjectIDs),
			Version: r.Number, CreatedAt: f.CreatedAt, UpdatedAt: r.CreatedAt}
		state[f.ID] = gf
		shown[f.ID] = project == "" || slices.Contains(r.ProjectIDs, project)
	}
	ids := make([]string, 0, len(state))
	for id := range state {
		if shown[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return features[ids[i]].Number < features[ids[j]].Number })
	for _, id := range ids {
		g.Features = append(g.Features, state[id])
	}
	links, err := fs.Store.FeatureLinks(ctx, s.org.ID)
	if err != nil {
		return FeatureGraph{}, err
	}
	for _, l := range links {
		from, to := features[l.FromID], features[l.ToID]
		if !s.sees(from.ProjectIDs) || !s.sees(to.ProjectIDs) {
			continue
		}
		summary := fmt.Sprintf("%s %s %s", from.Key, l.Type, to.Key)
		g.Changes = append(g.Changes, GraphChange{At: l.CreatedAt, FeatureKey: from.Key, Kind: "linked", Summary: summary})
		if !l.RemovedAt.IsZero() {
			g.Changes = append(g.Changes, GraphChange{At: l.RemovedAt, FeatureKey: from.Key, Kind: "unlinked", Summary: summary})
		}
		if l.ValidAt(at) && shown[l.FromID] && shown[l.ToID] {
			g.Links = append(g.Links, FeatureLinkView{Link: l, FromKey: from.Key, ToKey: to.Key})
		}
	}
	sort.SliceStable(g.Changes, func(i, j int) bool { return g.Changes[i].At.Before(g.Changes[j].At) })
	return g, nil
}

func revisionSummary(r feature.Revision) string {
	if r.Number == 1 {
		return "created “" + r.Title + "”"
	}
	if r.Reason != "" {
		return strings.TrimSpace(r.Reason)
	}
	return fmt.Sprintf("revision %d (%s)", r.Number, r.Status)
}

// Policy returns how agents of a project (projectID "": of the
// organization) may change features: the project's override, else the
// organization's policy.
func (fs *Features) Policy(ctx context.Context, org tenancy.Organization, projectID string) (feature.Policy, error) {
	var project feature.Policy
	if projectID != "" && fs.Execution != nil {
		x, err := fs.Execution.ExecutionSettings(ctx, projectID)
		switch {
		case err == nil:
			project = feature.Policy(x.FeaturePolicy)
		case !errors.Is(err, ErrNotFound):
			return "", err
		}
	}
	return feature.EffectivePolicy(feature.Policy(org.FeaturePolicy), project), nil
}

// ReviewView is a revision waiting for review, with the feature's title
// and the state before it (zero for a creation) to compare against.
type ReviewView struct {
	RevisionView
	FeatureTitle string
	Previous     RevisionView
}

// Reviews returns the revisions of features the caller can see that wait
// for review, oldest first.
func (fs *Features) Reviews(ctx context.Context, orgKey string) ([]ReviewView, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return nil, err
	}
	pending, err := fs.Store.PendingRevisions(ctx, s.org.ID)
	if err != nil {
		return nil, err
	}
	features, err := fs.byID(ctx, s)
	if err != nil {
		return nil, err
	}
	out := []ReviewView{}
	history := map[string][]feature.Revision{}
	for _, r := range pending {
		f := features[r.FeatureID]
		if !s.sees(f.ProjectIDs) {
			continue
		}
		if _, ok := history[f.ID]; !ok {
			if history[f.ID], err = fs.Store.FeatureRevisions(ctx, f.ID); err != nil {
				return nil, err
			}
		}
		v := ReviewView{RevisionView: s.revisionView(f, r), FeatureTitle: f.Title}
		for _, prev := range history[f.ID] {
			if prev.Number == r.Number-1 {
				v.Previous = s.revisionView(f, prev)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (s featureScope) revisionView(f feature.Feature, r feature.Revision) RevisionView {
	return RevisionView{Revision: r, FeatureKey: f.Key, ProjectKeys: s.projectKeys(r.ProjectIDs)}
}

// ReviewInput is a human's review of a revision.
type ReviewInput struct {
	Action  string // "confirm" or "revert"
	Comment string
}

// Review confirms or reverts a revision waiting for review. Reverting
// appends a revision restoring the state before it (a reverted creation
// removes the feature); only the latest revision can be reverted. Only
// humans review.
func (fs *Features) Review(ctx context.Context, orgKey, key string, number int64, in ReviewInput) (RevisionView, error) {
	s, err := fs.scope(ctx, orgKey)
	if err != nil {
		return RevisionView{}, err
	}
	if _, planner := PlannerSessionOf(ctx); planner || s.id.Kind != auth.KindHuman {
		return RevisionView{}, fmt.Errorf("%w: only a human can review", ErrForbidden)
	}
	if in.Action != "confirm" && in.Action != "revert" {
		return RevisionView{}, invalid(fmt.Errorf("action %q must be confirm or revert", in.Action))
	}
	if err := feature.ValidateReason(in.Comment); err != nil {
		return RevisionView{}, invalid(err)
	}
	f, err := fs.load(ctx, s, key)
	if err != nil {
		return RevisionView{}, err
	}
	if err := fs.canWrite(ctx, s, f.ProjectIDs); err != nil {
		return RevisionView{}, err
	}
	revs, err := fs.Store.FeatureRevisions(ctx, f.ID) // newest first
	if err != nil {
		return RevisionView{}, err
	}
	i := slices.IndexFunc(revs, func(r feature.Revision) bool { return r.Number == number })
	if i < 0 {
		return RevisionView{}, fmt.Errorf("revision %d of %s: %w", number, key, ErrNotFound)
	}
	rev := revs[i]
	if rev.Review != feature.ReviewPending {
		return RevisionView{}, fmt.Errorf("%w: revision %d of %s does not wait for review", ErrConflict, number, key)
	}
	now := fs.Now()
	mark := ReviewMark{FeatureID: f.ID, Number: number, By: s.id.Subject, At: now}
	payload := map[string]any{"key": f.Key, "revision": number}
	if in.Comment != "" {
		payload["comment"] = in.Comment
	}
	if in.Action == "confirm" {
		mark.Review = feature.ReviewConfirmed
		e := event.Event{Organization: s.org.ID, EntityType: "feature", EntityID: f.ID, Type: "feature.revision_confirmed",
			Actor: actorOf(s.id), OccurredAt: now, Payload: mustJSON(payload)}
		if err := fs.Store.ReviewRevision(ctx, mark, e); err != nil {
			return RevisionView{}, err
		}
	} else {
		if f.Version != number {
			return RevisionView{}, fmt.Errorf("%w: %s has later revisions; edit it instead", ErrConflict, key)
		}
		mark.Review = feature.ReviewReverted
		next := f
		if i+1 < len(revs) {
			prev := revs[i+1]
			next.Title, next.Description, next.Status, next.ProjectIDs = prev.Title, prev.Description, prev.Status, prev.ProjectIDs
		} else {
			next.Status = feature.StatusRemoved
		}
		next.Version, next.UpdatedAt = f.Version+1, now
		reason := fmt.Sprintf("Reverted revision %d", number)
		if in.Comment != "" {
			reason += ": " + in.Comment
		}
		w := fs.write(next, actorOf(s.id), reason, feature.Cause{Kind: "revert", Ref: strconv.FormatInt(number, 10)},
			"feature.revision_reverted", payload)
		w.Reviews = &mark
		if err := fs.Store.UpdateFeature(ctx, w, f.Version); err != nil {
			return RevisionView{}, err
		}
	}
	rev.Review, rev.ReviewedBy, rev.ReviewedAt = mark.Review, mark.By, mark.At
	return s.revisionView(f, rev), nil
}

// TicketFeatures returns the features a ticket changes.
func (fs *Features) TicketFeatures(ctx context.Context, itemKey string) ([]FeatureView, error) {
	it, s, err := fs.ticket(ctx, itemKey, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	fts, err := fs.Store.TicketFeatures(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	out := make([]FeatureView, len(fts))
	for i, f := range fts {
		out[i] = s.view(f)
	}
	return out, nil
}

// SetTicketFeatures replaces the features a ticket changes; they must be
// features of its organization.
func (fs *Features) SetTicketFeatures(ctx context.Context, itemKey string, featureKeys []string) ([]FeatureView, error) {
	it, s, err := fs.ticket(ctx, itemKey, ActTrackerWrite)
	if err != nil {
		return nil, err
	}
	if it.Kind != tracker.KindTicket {
		return nil, invalid(fmt.Errorf("%s is a %s; only tickets change features", it.Key, it.Kind))
	}
	ids := []string{}
	views := []FeatureView{}
	for _, k := range featureKeys {
		f, err := fs.load(ctx, s, k)
		if errors.Is(err, ErrNotFound) {
			return nil, invalid(fmt.Errorf("feature %s does not exist", k))
		}
		if err != nil {
			return nil, err
		}
		if slices.Contains(ids, f.ID) {
			return nil, invalid(fmt.Errorf("feature %s is listed twice", k))
		}
		ids = append(ids, f.ID)
		views = append(views, s.view(f))
	}
	now := fs.Now()
	e := event.Event{Organization: s.org.ID, Project: it.ProjectID, EntityType: "item", EntityID: it.ID,
		Type: "item.features_set", Actor: actorIn(ctx, s.id), OccurredAt: now,
		Payload: mustJSON(map[string]any{"features": featureKeys})}
	if err := fs.Store.SetTicketFeatures(ctx, it.ID, ids, now, e); err != nil {
		return nil, err
	}
	return views, nil
}

// ticket loads an item and authorizes a on its project.
func (fs *Features) ticket(ctx context.Context, itemKey string, a Action) (tracker.Item, featureScope, error) {
	if fs.Items == nil {
		return tracker.Item{}, featureScope{}, fmt.Errorf("%w: ticket links are not available", ErrNotFound)
	}
	id, err := caller(ctx)
	if err != nil {
		return tracker.Item{}, featureScope{}, err
	}
	it, err := fs.Items.ItemByKey(ctx, itemKey)
	if err != nil {
		return tracker.Item{}, featureScope{}, err
	}
	p, err := fs.Tenancy.ProjectByID(ctx, it.ProjectID)
	if err != nil {
		return tracker.Item{}, featureScope{}, err
	}
	org, err := fs.Tenancy.OrganizationByID(ctx, p.OrganizationID)
	if err != nil {
		return tracker.Item{}, featureScope{}, err
	}
	if err := fs.Authz.Authorize(ctx, id, a, Scope{Organization: org.Key, Project: p.Key}); err != nil {
		return tracker.Item{}, featureScope{}, err
	}
	s, err := fs.scope(ctx, org.Key)
	return it, s, err
}

// ballet is the actor of changes Ballet makes on its own.
var ballet = event.Actor{Kind: event.ActorSystem, Subject: "ballet"}

// SyncStatus moves every feature whose tickets progressed to the status
// they imply (feature.DeliveryStatus), recording a revision caused by the
// tickets. It returns the number of features changed.
func (fs *Features) SyncStatus(ctx context.Context) (int, error) {
	states, err := fs.Store.DeliveryStates(ctx)
	if err != nil {
		return 0, err
	}
	byFeature := map[string][]FeatureTicketState{}
	var order []string
	for _, st := range states {
		if _, ok := byFeature[st.FeatureID]; !ok {
			order = append(order, st.FeatureID)
		}
		byFeature[st.FeatureID] = append(byFeature[st.FeatureID], st)
	}
	changed := 0
	for _, id := range order {
		f, err := fs.Store.FeatureByID(ctx, id)
		if err != nil {
			return changed, err
		}
		tickets := byFeature[id]
		ss := make([]tracker.State, len(tickets))
		for i, t := range tickets {
			ss[i] = t.State
		}
		next := feature.DeliveryStatus(f.Status, ss)
		if next == f.Status {
			continue
		}
		var keys []string
		for _, t := range tickets {
			if (next == feature.StatusLive && t.State == tracker.StateDone) || (next != feature.StatusLive && !tracker.Resolved(t.State)) {
				keys = append(keys, t.TicketKey)
			}
		}
		reason := map[feature.Status]string{
			feature.StatusInProgress: "Work started: ",
			feature.StatusChanging:   "Being changed: ",
			feature.StatusLive:       "Delivered: ",
		}[next] + strings.Join(keys, ", ")
		updated := f
		updated.Status, updated.Version, updated.UpdatedAt = next, f.Version+1, fs.Now()
		w := fs.write(updated, ballet, reason, feature.Cause{Kind: "ticket", Ref: strings.Join(keys, ",")},
			"feature.updated", map[string]any{"status": next})
		err = fs.Store.UpdateFeature(ctx, w, f.Version)
		if errors.Is(err, ErrConflict) {
			continue // changed meanwhile; the next pass sees it
		}
		if err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// StatusLoop runs SyncStatus every interval until ctx ends.
func (fs *Features) StatusLoop(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := fs.SyncStatus(ctx); err != nil && ctx.Err() == nil {
				logger.ErrorContext(ctx, "sync feature status failed", "error", err)
			}
		}
	}
}
