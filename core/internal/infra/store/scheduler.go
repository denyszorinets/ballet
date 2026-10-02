package store

import (
	"context"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// RunnableTickets returns ready tickets of projects with a repository whose
// blockers (on the ticket or its epic) are all resolved, oldest first.
func (s *Store) RunnableTickets(ctx context.Context) ([]tracker.Item, error) {
	rows, err := s.db.Query(ctx, `SELECT `+itemCols+` FROM items i
		WHERE i.kind = 'ticket' AND i.state = 'ready'
		AND EXISTS (SELECT 1 FROM project_execution pe WHERE pe.project_id = i.project_id AND pe.repo_url != '')
		AND NOT EXISTS (
			SELECT 1 FROM dependencies d JOIN items b ON b.id = d.from_id
			WHERE d.type = 'blocks' AND (d.to_id = i.id OR d.to_id = i.epic_id)
			AND b.state NOT IN ('done', 'cancelled'))
		ORDER BY i.created_at, i.number`)
	if err != nil {
		return nil, fmt.Errorf("runnable tickets: %w", err)
	}
	defer rows.Close()
	var out []tracker.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("runnable tickets: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// BusyFlows counts, per project, the flows that occupy a slot: running ones
// and those waiting for checks (not for a human).
func (s *Store) BusyFlows(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `SELECT project_id, COUNT(*) FROM flows
		WHERE status = 'running' OR (status = 'waiting' AND waiting = 'checks') GROUP BY project_id`)
	if err != nil {
		return nil, fmt.Errorf("busy flows: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, fmt.Errorf("busy flows: %w", err)
		}
		out[p] = n
	}
	return out, rows.Err()
}
