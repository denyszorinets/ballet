package store

import (
	"context"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

const depCols = `id, project_id, from_id, to_id, type, created_at`

// ProjectGraph returns the project's dependency edges and its graph version.
// Pass the version to AddDependency to detect concurrent changes.
func (s *Store) ProjectGraph(ctx context.Context, projectID string) ([]tracker.Dependency, int64, error) {
	var version int64
	if err := s.db.QueryRow(ctx, `SELECT graph_version FROM projects WHERE id = ?`, projectID).Scan(&version); err != nil {
		return nil, 0, mapReadErr("project graph", err)
	}
	deps, err := s.queryDeps(ctx, `SELECT `+depCols+` FROM dependencies WHERE project_id = ?`, projectID)
	return deps, version, err
}

// ItemDependencies returns the edges touching an item.
func (s *Store) ItemDependencies(ctx context.Context, itemID string) ([]tracker.Dependency, error) {
	return s.queryDeps(ctx, `SELECT `+depCols+` FROM dependencies WHERE from_id = ? OR to_id = ? ORDER BY created_at`, itemID, itemID)
}

// Dependency returns the edge with id.
func (s *Store) Dependency(ctx context.Context, id string) (tracker.Dependency, error) {
	d, err := scanDep(s.db.QueryRow(ctx, `SELECT `+depCols+` FROM dependencies WHERE id = ?`, id))
	return d, mapReadErr("dependency "+id, err)
}

// AddDependency inserts d if the project's graph is still at graphVersion,
// and records e. A concurrent graph change yields ErrConflict.
func (s *Store) AddDependency(ctx context.Context, d tracker.Dependency, graphVersion int64, e event.Event) error {
	return mapWriteErr("add dependency", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE projects SET graph_version = graph_version + 1 WHERE id = ? AND graph_version = ?`,
			d.ProjectID, graphVersion),
		insertDepStmt(d),
		s.AppendEvent(e),
	))
}

func insertDepStmt(d tracker.Dependency) sqlstore.Stmt {
	return sqlstore.Exec(`INSERT INTO dependencies (`+depCols+`) VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.ProjectID, d.FromID, d.ToID, string(d.Type), formatTime(d.CreatedAt))
}

// RemoveDependency deletes d and records e. Removing an edge cannot create
// a cycle, so it only bumps the graph version.
func (s *Store) RemoveDependency(ctx context.Context, d tracker.Dependency, e event.Event) error {
	return mapWriteErr("remove dependency", s.db.Batch(ctx,
		sqlstore.ExecOne(`DELETE FROM dependencies WHERE id = ?`, d.ID),
		sqlstore.Exec(`UPDATE projects SET graph_version = graph_version + 1 WHERE id = ?`, d.ProjectID),
		s.AppendEvent(e),
	))
}

// ListRunnable returns the project's tickets that are ready and whose
// blockers are all resolved (done or cancelled), ordered by number.
func (s *Store) ListRunnable(ctx context.Context, projectID string) ([]tracker.Item, error) {
	rows, err := s.db.Query(ctx, `SELECT `+itemCols+` FROM items t
		WHERE t.project_id = ? AND t.kind = 'ticket' AND t.state = 'ready'
		AND NOT EXISTS (
			SELECT 1 FROM dependencies d JOIN items b ON b.id = d.from_id
			WHERE d.to_id = t.id AND d.type = 'blocks' AND b.state NOT IN ('done', 'cancelled')
		)
		ORDER BY t.number`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list runnable: %w", err)
	}
	defer rows.Close()
	var out []tracker.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("list runnable: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) queryDeps(ctx context.Context, q string, args ...any) ([]tracker.Dependency, error) {
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}
	defer rows.Close()
	var out []tracker.Dependency
	for rows.Next() {
		d, err := scanDep(rows)
		if err != nil {
			return nil, fmt.Errorf("list dependencies: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanDep(r scanner) (tracker.Dependency, error) {
	var d tracker.Dependency
	var typ, created string
	if err := r.Scan(&d.ID, &d.ProjectID, &d.FromID, &d.ToID, &typ, &created); err != nil {
		return tracker.Dependency{}, err
	}
	d.Type = tracker.DepType(typ)
	var err error
	d.CreatedAt, err = parseTime(created)
	return d, err
}

// BlockedBehind counts the unresolved items an item blocks, directly or
// through other items.
func (s *Store) BlockedBehind(ctx context.Context, itemID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `WITH RECURSIVE behind(id) AS (
			SELECT to_id FROM dependencies WHERE from_id = ? AND type = 'blocks'
			UNION
			SELECT d.to_id FROM dependencies d JOIN behind b ON d.from_id = b.id WHERE d.type = 'blocks')
		SELECT COUNT(*) FROM behind JOIN items i ON i.id = behind.id WHERE i.state NOT IN ('done', 'cancelled')`,
		itemID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("blocked behind: %w", err)
	}
	return n, nil
}
