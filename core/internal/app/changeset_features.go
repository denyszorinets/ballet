package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/kit/auth"
)

// featureBuild applies a changeset's feature operations (ADR-0028) to the
// organization's feature map in memory, producing the writes. Keys of
// created features follow the organization's sequence from first.
type featureBuild struct {
	fs        *Features
	ch        changeset.Changeset
	s         featureScope
	project   tenancy.Project
	authorize bool // check the applying human's write permission
	first     int64
	next      int64
	byKey     map[string]feature.Feature // existing features, as changed so far
	created   map[string]feature.Feature // by ref
	updated   map[string]bool            // feature IDs
	links     []feature.Link             // valid links, existing and added
}

func (cs *Changesets) featureBuild(ctx context.Context, ch changeset.Changeset, org tenancy.Organization, p tenancy.Project, id identity) (*featureBuild, error) {
	b := &featureBuild{fs: cs.Features, ch: ch, project: p, authorize: id.Kind == auth.KindHuman,
		byKey: map[string]feature.Feature{}, created: map[string]feature.Feature{}, updated: map[string]bool{}}
	if !usesFeatures(ch) {
		return b, nil
	}
	if cs.Features == nil {
		return nil, invalid(errors.New("the feature map is not available"))
	}
	fs := cs.Features
	ps, err := fs.Tenancy.ListProjects(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	b.s = featureScope{id: id, org: org, projects: map[string]tenancy.Project{}, all: true}
	for _, pr := range ps {
		b.s.projects[pr.ID] = pr
	}
	if b.first, err = fs.Store.NextFeatureNumber(ctx, org.ID); err != nil {
		return nil, err
	}
	b.next = b.first
	all, err := fs.Store.ListFeatures(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	for _, f := range all {
		b.byKey[f.Key] = f
	}
	links, err := fs.Store.FeatureLinks(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if l.RemovedAt.IsZero() {
			b.links = append(b.links, l)
		}
	}
	return b, nil
}

func usesFeatures(ch changeset.Changeset) bool {
	for _, op := range ch.Ops {
		switch {
		case op.Feature != nil, op.FeatureUpdate != nil, op.FeatureLink != nil,
			op.Create != nil && len(op.Create.Features) > 0, op.Update != nil && op.Update.Features != nil:
			return true
		}
	}
	return false
}

// resolve returns the feature a reference points to.
func (b *featureBuild) resolve(ref string) (feature.Feature, error) {
	if r, ok := changeset.IsRef(ref); ok {
		f, ok := b.created[r]
		if !ok {
			return feature.Feature{}, fmt.Errorf("%s is not approved", ref)
		}
		return f, nil
	}
	f, ok := b.byKey[ref]
	if !ok {
		return feature.Feature{}, fmt.Errorf("feature %s does not exist in organization %s", ref, b.s.org.Key)
	}
	return f, nil
}

// ids resolves a ticket's feature references.
func (b *featureBuild) ids(refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		f, err := b.resolve(r)
		if err != nil {
			return nil, err
		}
		out = append(out, f.ID)
	}
	return out, nil
}

func (b *featureBuild) canWrite(ctx context.Context, projectIDs []string) error {
	if !b.authorize {
		return nil
	}
	if err := b.fs.canWrite(ctx, b.s, projectIDs); err != nil {
		return storeError{err}
	}
	return nil
}

func (b *featureBuild) reason() string { return "Changeset “" + b.ch.Title + "”" }

func (b *featureBuild) cause() feature.Cause { return feature.Cause{Kind: "changeset", Ref: b.ch.ID} }

// apply builds one feature operation into a.
func (b *featureBuild) apply(ctx context.Context, op changeset.Op, a *ChangesetApplication, res *changeset.Result) error {
	fs := b.fs
	actor := actorIn(ctx, b.s.id)
	switch op.Kind {
	case changeset.OpCreateFeature:
		in := op.Feature
		keys := in.Projects
		if len(keys) == 0 {
			keys = []string{b.project.Key}
		}
		pids, err := b.s.projectIDs(keys)
		if err != nil {
			return err
		}
		if err := b.canWrite(ctx, pids); err != nil {
			return err
		}
		status := in.Status
		if status == "" {
			status = feature.StatusPlanned
		}
		now := fs.Now()
		f := feature.Feature{
			ID: fs.NewID(), OrganizationID: b.s.org.ID, Number: b.next, Key: feature.Key(b.next),
			Title: in.Title, Description: in.Description, Status: status, ProjectIDs: pids,
			CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		if err := f.Validate(); err != nil {
			return err
		}
		b.next++
		b.created[op.Ref] = f
		res.Key = f.Key
		payload := map[string]any{"title": f.Title, "changeset": b.ch.ID}
		a.FeatureCreates = append(a.FeatureCreates, fs.write(f, actor, b.reason(), b.cause(), "feature.created", payload))

	case changeset.OpUpdateFeature:
		in := op.FeatureUpdate
		f, err := b.resolve(in.Feature)
		if err != nil {
			return err
		}
		if b.updated[f.ID] {
			return fmt.Errorf("%s is updated twice", f.Key)
		}
		b.updated[f.ID] = true
		if err := b.canWrite(ctx, f.ProjectIDs); err != nil {
			return err
		}
		next, changed, err := fs.apply(b.s, f, UpdateFeatureInput{Version: f.Version, Title: in.Title,
			Description: in.Description, Status: in.Status, ProjectKeys: in.Projects, Reason: b.reason()})
		if err != nil {
			return err
		}
		if !sameSet(next.ProjectIDs, f.ProjectIDs) {
			if err := b.canWrite(ctx, next.ProjectIDs); err != nil {
				return err
			}
		}
		changed["changeset"] = b.ch.ID
		b.byKey[f.Key] = next
		res.Key = f.Key
		a.FeatureUpdates = append(a.FeatureUpdates, FeatureUpdateWrite{
			FeatureWrite:    fs.write(next, actor, b.reason(), b.cause(), "feature.updated", changed),
			ExpectedVersion: f.Version,
		})

	case changeset.OpLinkFeatures:
		in := op.FeatureLink
		from, err := b.resolve(in.From)
		if err != nil {
			return err
		}
		to, err := b.resolve(in.To)
		if err != nil {
			return err
		}
		if err := b.canWrite(ctx, from.ProjectIDs); err != nil {
			return err
		}
		l := feature.Link{ID: fs.NewID(), OrganizationID: b.s.org.ID, FromID: from.ID, ToID: to.ID, Type: in.Type,
			CreatedBy: actor, CreatedAt: fs.Now()}
		if err := l.Validate(); err != nil {
			return err
		}
		for _, e := range b.links {
			if e.FromID == l.FromID && e.ToID == l.ToID && e.Type == l.Type {
				return fmt.Errorf("%s already %s %s", from.Key, l.Type, to.Key)
			}
		}
		b.links = append(b.links, l)
		res.FeatureLinkID = l.ID
		a.FeatureLinks = append(a.FeatureLinks, FeatureLinkWrite{Link: l, Event: linkEvent(l, "feature.linked", actor, from.Key, to.Key)})
	}
	return nil
}
