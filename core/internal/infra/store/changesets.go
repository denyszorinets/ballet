package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

const changesetCols = `id, project_id, title, summary, ops, status, proposed_by, created_at, decided_by, decided_at,
	approved, results, version`

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// CreateChangeset inserts a proposed changeset and records e.
func (s *Store) CreateChangeset(ctx context.Context, cs changeset.Changeset, e event.Event) error {
	ops, err := toJSON(cs.Ops)
	if err != nil {
		return fmt.Errorf("create changeset: %w", err)
	}
	by, err := toJSON(cs.ProposedBy)
	if err != nil {
		return fmt.Errorf("create changeset: %w", err)
	}
	return mapWriteErr("create changeset", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO changesets (id, project_id, title, summary, ops, status, proposed_by, created_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			cs.ID, cs.ProjectID, cs.Title, cs.Summary, ops, string(cs.Status), by, formatTime(cs.CreatedAt), cs.Version),
		s.AppendEvent(e),
	))
}

// Changeset returns the changeset with id.
func (s *Store) Changeset(ctx context.Context, id string) (changeset.Changeset, error) {
	cs, err := scanChangeset(s.db.QueryRow(ctx, `SELECT `+changesetCols+` FROM changesets WHERE id = ?`, id))
	return cs, mapReadErr("changeset "+id, err)
}

// ListChangesets returns a project's changesets, newest first; an empty
// status returns all.
func (s *Store) ListChangesets(ctx context.Context, projectID string, status changeset.Status) ([]changeset.Changeset, error) {
	rows, err := s.db.Query(ctx, `SELECT `+changesetCols+` FROM changesets
		WHERE project_id = ? AND (? = '' OR status = ?) ORDER BY created_at DESC, id DESC`,
		projectID, string(status), string(status))
	if err != nil {
		return nil, fmt.Errorf("list changesets: %w", err)
	}
	defer rows.Close()
	var out []changeset.Changeset
	for rows.Next() {
		cs, err := scanChangeset(rows)
		if err != nil {
			return nil, fmt.Errorf("list changesets: %w", err)
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// NextItemNumber returns the number the project's next item will get.
func (s *Store) NextItemNumber(ctx context.Context, projectID string) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT next_item_number FROM projects WHERE id = ?`, projectID).Scan(&n)
	return n, mapReadErr("project "+projectID, err)
}

// DecideChangeset stores the decided changeset if it is still proposed at
// expectedVersion and applies a, all in one atomic batch. The item
// sequence, the graph version (with dependencies) and the versions of
// updated items are guarded; any concurrent change yields ErrConflict and
// nothing is written.
func (s *Store) DecideChangeset(ctx context.Context, cs changeset.Changeset, expectedVersion int64, a app.ChangesetApplication, e event.Event) error {
	approved, err := toJSON(nonNil(cs.Approved))
	if err != nil {
		return fmt.Errorf("decide changeset: %w", err)
	}
	results, err := toJSON(nonNil(cs.Results))
	if err != nil {
		return fmt.Errorf("decide changeset: %w", err)
	}
	by, err := toJSON(cs.DecidedBy)
	if err != nil {
		return fmt.Errorf("decide changeset: %w", err)
	}
	stmts := []sqlstore.Stmt{
		sqlstore.ExecOne(`UPDATE changesets SET status = ?, decided_by = ?, decided_at = ?, approved = ?, results = ?,
				version = ? WHERE id = ? AND version = ? AND status = 'proposed'`,
			string(cs.Status), by, formatTime(cs.DecidedAt), approved, results, cs.Version, cs.ID, expectedVersion),
	}
	if len(a.Creates) > 0 {
		// The keys of created items were predicted from this value.
		stmts = append(stmts, sqlstore.ExecOne(`UPDATE projects SET next_item_number = next_item_number
			WHERE id = ? AND next_item_number = ?`, a.ProjectID, a.NextItemNumber))
	}
	if len(a.Dependencies) > 0 {
		stmts = append(stmts, sqlstore.ExecOne(`UPDATE projects SET graph_version = graph_version + 1
			WHERE id = ? AND graph_version = ?`, a.ProjectID, a.GraphVersion))
	}
	for _, w := range a.Creates {
		create, err := createItemStmts(w.Item)
		if err != nil {
			return fmt.Errorf("decide changeset: %w", err)
		}
		stmts = append(append(stmts, create...), s.AppendEvent(w.Event))
	}
	for _, w := range a.Updates {
		update, err := updateItemStmt(w.Item, w.ExpectedVersion)
		if err != nil {
			return fmt.Errorf("decide changeset: %w", err)
		}
		stmts = append(stmts, update, s.AppendEvent(w.Event))
	}
	for _, w := range a.Dependencies {
		stmts = append(stmts, insertDepStmt(w.Dependency), s.AppendEvent(w.Event))
	}
	stmts = append(stmts, s.AppendEvent(e))
	return mapWriteErr("decide changeset", s.db.Batch(ctx, stmts...))
}

func scanChangeset(r scanner) (changeset.Changeset, error) {
	var cs changeset.Changeset
	var ops, status, proposedBy, created, approved, results string
	var decidedBy, decidedAt sql.NullString
	if err := r.Scan(&cs.ID, &cs.ProjectID, &cs.Title, &cs.Summary, &ops, &status, &proposedBy, &created,
		&decidedBy, &decidedAt, &approved, &results, &cs.Version); err != nil {
		return changeset.Changeset{}, err
	}
	cs.Status = changeset.Status(status)
	var err error
	if cs.CreatedAt, err = parseTime(created); err != nil {
		return changeset.Changeset{}, err
	}
	if decidedAt.Valid {
		if cs.DecidedAt, err = parseTime(decidedAt.String); err != nil {
			return changeset.Changeset{}, err
		}
	}
	for _, f := range []struct {
		src string
		dst any
	}{{ops, &cs.Ops}, {proposedBy, &cs.ProposedBy}, {approved, &cs.Approved}, {results, &cs.Results}} {
		if err := json.Unmarshal([]byte(f.src), f.dst); err != nil {
			return changeset.Changeset{}, err
		}
	}
	if decidedBy.Valid {
		if err := json.Unmarshal([]byte(decidedBy.String), &cs.DecidedBy); err != nil {
			return changeset.Changeset{}, err
		}
	}
	return cs, nil
}
