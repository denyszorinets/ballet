package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// NextFeatureNumber returns the number the organization's next feature
// gets.
func (s *Store) NextFeatureNumber(ctx context.Context, organizationID string) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT next_feature_number FROM organizations WHERE id = ?`, organizationID).Scan(&n)
	return n, mapReadErr("organization "+organizationID, err)
}

// CreateFeatures inserts features with their first revisions and events,
// advancing the organization's feature sequence from next; ErrConflict
// when the sequence moved meanwhile.
func (s *Store) CreateFeatures(ctx context.Context, organizationID string, next int64, ws []app.FeatureWrite) error {
	stmts := append([]sqlstore.Stmt{}, featureSequenceStmt(organizationID, next, len(ws)))
	for _, w := range ws {
		ss, err := s.createFeatureStmts(w)
		if err != nil {
			return fmt.Errorf("create feature: %w", err)
		}
		stmts = append(stmts, ss...)
	}
	return mapWriteErr("create features", s.db.Batch(ctx, stmts...))
}

// featureSequenceStmt advances the sequence by n if it is still at next.
func featureSequenceStmt(organizationID string, next int64, n int) sqlstore.Stmt {
	return sqlstore.ExecOne(`UPDATE organizations SET next_feature_number = next_feature_number + ?
		WHERE id = ? AND next_feature_number = ?`, n, organizationID, next)
}

func (s *Store) createFeatureStmts(w app.FeatureWrite) ([]sqlstore.Stmt, error) {
	f := w.Feature
	rev, err := revisionStmt(w.Revision)
	if err != nil {
		return nil, err
	}
	stmts := []sqlstore.Stmt{sqlstore.Exec(`INSERT INTO features
			(id, organization_id, number, key, title, description, status, created_at, updated_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.OrganizationID, f.Number, f.Key, f.Title, f.Description, string(f.Status),
		formatTime(f.CreatedAt), formatTime(f.UpdatedAt), f.Version)}
	stmts = append(stmts, featureProjectStmts(f)...)
	return append(stmts, rev, s.AppendEvent(w.Event)), nil
}

func featureProjectStmts(f feature.Feature) []sqlstore.Stmt {
	stmts := []sqlstore.Stmt{sqlstore.Exec(`DELETE FROM feature_projects WHERE feature_id = ?`, f.ID)}
	for _, p := range f.ProjectIDs {
		stmts = append(stmts, sqlstore.Exec(`INSERT INTO feature_projects (feature_id, project_id) VALUES (?, ?)`, f.ID, p))
	}
	return stmts
}

func revisionStmt(r feature.Revision) (sqlstore.Stmt, error) {
	projects, err := json.Marshal(nonNil(r.ProjectIDs))
	if err != nil {
		return sqlstore.Stmt{}, err
	}
	reviewedAt := ""
	if !r.ReviewedAt.IsZero() {
		reviewedAt = formatTime(r.ReviewedAt)
	}
	return sqlstore.Exec(`INSERT INTO feature_revisions
			(feature_id, number, title, description, status, projects, author_kind, author_sub, acting_for,
			 reason, cause_kind, cause_ref, review, reviewed_by, reviewed_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.FeatureID, r.Number, r.Title, r.Description, string(r.Status), string(projects),
		string(r.Author.Kind), r.Author.Subject, r.Author.ActingFor, r.Reason, r.Cause.Kind, r.Cause.Ref,
		string(r.Review), r.ReviewedBy, reviewedAt, formatTime(r.CreatedAt)), nil
}

// UpdateFeature stores w if the feature is still at expected.
func (s *Store) UpdateFeature(ctx context.Context, w app.FeatureWrite, expected int64) error {
	stmts, err := s.updateFeatureStmts(w, expected)
	if err != nil {
		return fmt.Errorf("update feature: %w", err)
	}
	return mapWriteErr("update feature", s.db.Batch(ctx, stmts...))
}

func (s *Store) updateFeatureStmts(w app.FeatureWrite, expected int64) ([]sqlstore.Stmt, error) {
	f := w.Feature
	rev, err := revisionStmt(w.Revision)
	if err != nil {
		return nil, err
	}
	stmts := []sqlstore.Stmt{sqlstore.ExecOne(`UPDATE features SET title = ?, description = ?, status = ?,
			updated_at = ?, version = ? WHERE id = ? AND version = ?`,
		f.Title, f.Description, string(f.Status), formatTime(f.UpdatedAt), f.Version, f.ID, expected)}
	stmts = append(stmts, featureProjectStmts(f)...)
	return append(stmts, rev, s.AppendEvent(w.Event)), nil
}

const featureCols = `f.id, f.organization_id, f.number, f.key, f.title, f.description, f.status, f.created_at,
	f.updated_at, f.version, (SELECT coalesce(json_group_array(project_id), '[]') FROM
	(SELECT project_id FROM feature_projects WHERE feature_id = f.id ORDER BY project_id))`

func scanFeature(r scanner) (feature.Feature, error) {
	var f feature.Feature
	var status, created, updated, projects string
	if err := r.Scan(&f.ID, &f.OrganizationID, &f.Number, &f.Key, &f.Title, &f.Description, &status,
		&created, &updated, &f.Version, &projects); err != nil {
		return feature.Feature{}, err
	}
	f.Status = feature.Status(status)
	var err error
	if f.CreatedAt, err = parseTime(created); err != nil {
		return feature.Feature{}, err
	}
	if f.UpdatedAt, err = parseTime(updated); err != nil {
		return feature.Feature{}, err
	}
	if err := json.Unmarshal([]byte(projects), &f.ProjectIDs); err != nil {
		return feature.Feature{}, err
	}
	return f, nil
}

// FeatureByKey returns an organization's feature by key.
func (s *Store) FeatureByKey(ctx context.Context, organizationID, key string) (feature.Feature, error) {
	f, err := scanFeature(s.db.QueryRow(ctx, `SELECT `+featureCols+` FROM features f
		WHERE f.organization_id = ? AND f.key = ?`, organizationID, key))
	return f, mapReadErr("feature "+key, err)
}

// FeatureByID returns a feature by ID.
func (s *Store) FeatureByID(ctx context.Context, id string) (feature.Feature, error) {
	f, err := scanFeature(s.db.QueryRow(ctx, `SELECT `+featureCols+` FROM features f WHERE f.id = ?`, id))
	return f, mapReadErr("feature "+id, err)
}

// ListFeatures returns an organization's features by number.
func (s *Store) ListFeatures(ctx context.Context, organizationID string) ([]feature.Feature, error) {
	rows, err := s.db.Query(ctx, `SELECT `+featureCols+` FROM features f WHERE f.organization_id = ?
		ORDER BY f.number`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list features: %w", err)
	}
	defer rows.Close()
	out := []feature.Feature{}
	for rows.Next() {
		f, err := scanFeature(rows)
		if err != nil {
			return nil, fmt.Errorf("list features: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

const revisionCols = `feature_id, number, title, %s, status, projects, author_kind, author_sub, acting_for,
	reason, cause_kind, cause_ref, review, reviewed_by, reviewed_at, created_at`

func scanRevision(r scanner) (feature.Revision, error) {
	var rev feature.Revision
	var status, projects, kind, review, reviewedAt, created string
	if err := r.Scan(&rev.FeatureID, &rev.Number, &rev.Title, &rev.Description, &status, &projects, &kind,
		&rev.Author.Subject, &rev.Author.ActingFor, &rev.Reason, &rev.Cause.Kind, &rev.Cause.Ref, &review,
		&rev.ReviewedBy, &reviewedAt, &created); err != nil {
		return feature.Revision{}, err
	}
	rev.Status, rev.Author.Kind, rev.Review = feature.Status(status), event.ActorKind(kind), feature.Review(review)
	if err := json.Unmarshal([]byte(projects), &rev.ProjectIDs); err != nil {
		return feature.Revision{}, err
	}
	var err error
	if rev.CreatedAt, err = parseTime(created); err != nil {
		return feature.Revision{}, err
	}
	if reviewedAt != "" {
		if rev.ReviewedAt, err = parseTime(reviewedAt); err != nil {
			return feature.Revision{}, err
		}
	}
	return rev, nil
}

func (s *Store) revisions(ctx context.Context, query string, args ...any) ([]feature.Revision, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list feature revisions: %w", err)
	}
	defer rows.Close()
	out := []feature.Revision{}
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("list feature revisions: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FeatureRevisions returns a feature's revisions, newest first.
func (s *Store) FeatureRevisions(ctx context.Context, featureID string) ([]feature.Revision, error) {
	return s.revisions(ctx, `SELECT `+fmt.Sprintf(revisionCols, "description")+` FROM feature_revisions
		WHERE feature_id = ? ORDER BY number DESC`, featureID)
}

// OrganizationRevisions returns every revision of an organization's
// features without descriptions, oldest first.
func (s *Store) OrganizationRevisions(ctx context.Context, organizationID string) ([]feature.Revision, error) {
	revs, err := s.revisions(ctx, `SELECT `+fmt.Sprintf(revisionCols, "''")+` FROM feature_revisions
		WHERE feature_id IN (SELECT id FROM features WHERE organization_id = ?)`, organizationID)
	if err != nil {
		return nil, err
	}
	// Ordered here: stored times do not sort as text.
	slices.SortStableFunc(revs, func(a, b feature.Revision) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return int(a.Number - b.Number)
	})
	return revs, nil
}

// FeatureLinks returns an organization's links, removed ones included,
// oldest first.
func (s *Store) FeatureLinks(ctx context.Context, organizationID string) ([]feature.Link, error) {
	rows, err := s.db.Query(ctx, `SELECT id, organization_id, from_id, to_id, type, created_kind, created_sub,
			created_at, removed_kind, removed_sub, removed_at
		FROM feature_links WHERE organization_id = ?`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list feature links: %w", err)
	}
	defer rows.Close()
	out := []feature.Link{}
	for rows.Next() {
		var l feature.Link
		var typ, ck, created, rk, removed string
		if err := rows.Scan(&l.ID, &l.OrganizationID, &l.FromID, &l.ToID, &typ, &ck, &l.CreatedBy.Subject,
			&created, &rk, &l.RemovedBy.Subject, &removed); err != nil {
			return nil, fmt.Errorf("list feature links: %w", err)
		}
		l.Type, l.CreatedBy.Kind, l.RemovedBy.Kind = feature.LinkType(typ), event.ActorKind(ck), event.ActorKind(rk)
		if l.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if removed != "" {
			if l.RemovedAt, err = parseTime(removed); err != nil {
				return nil, err
			}
		}
		out = append(out, l)
	}
	slices.SortStableFunc(out, func(a, b feature.Link) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, rows.Err()
}

// AddFeatureLink inserts a link and records e.
func (s *Store) AddFeatureLink(ctx context.Context, l feature.Link, e event.Event) error {
	return mapWriteErr("add feature link", s.db.Batch(ctx, addLinkStmt(l), s.AppendEvent(e)))
}

func addLinkStmt(l feature.Link) sqlstore.Stmt {
	return sqlstore.Exec(`INSERT INTO feature_links
			(id, organization_id, from_id, to_id, type, created_kind, created_sub, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.OrganizationID, l.FromID, l.ToID, string(l.Type), string(l.CreatedBy.Kind), l.CreatedBy.Subject,
		formatTime(l.CreatedAt))
}

// RemoveFeatureLink marks a valid link removed and records e.
func (s *Store) RemoveFeatureLink(ctx context.Context, l feature.Link, e event.Event) error {
	return mapWriteErr("remove feature link", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE feature_links SET removed_kind = ?, removed_sub = ?, removed_at = ?
			WHERE id = ? AND removed_at = ''`,
			string(l.RemovedBy.Kind), l.RemovedBy.Subject, formatTime(l.RemovedAt), l.ID),
		s.AppendEvent(e)))
}
