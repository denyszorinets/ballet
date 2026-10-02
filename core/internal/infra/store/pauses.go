package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// SetPause records a pause (replacing one of the same scope) and e.
func (s *Store) SetPause(ctx context.Context, p app.Pause, e event.Event) error {
	return mapWriteErr("set pause", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT OR REPLACE INTO pauses (scope, reason, paused_by, paused_at) VALUES (?, ?, ?, ?)`,
			p.Scope, p.Reason, p.PausedBy, formatTime(p.PausedAt)),
		s.AppendEvent(e),
	))
}

// ClearPause removes a pause (ErrNotFound when there is none) and records e.
func (s *Store) ClearPause(ctx context.Context, scope string, e event.Event) error {
	err := s.db.Batch(ctx,
		sqlstore.ExecOne(`DELETE FROM pauses WHERE scope = ?`, scope),
		s.AppendEvent(e),
	)
	if errors.Is(err, sqlstore.ErrConflict) { // no such row
		return fmt.Errorf("%w: not paused", app.ErrNotFound)
	}
	return mapWriteErr("clear pause", err)
}

// Pauses returns every pause.
func (s *Store) Pauses(ctx context.Context) ([]app.Pause, error) {
	rows, err := s.db.Query(ctx, `SELECT scope, reason, paused_by, paused_at FROM pauses ORDER BY paused_at`)
	if err != nil {
		return nil, fmt.Errorf("pauses: %w", err)
	}
	defer rows.Close()
	var out []app.Pause
	for rows.Next() {
		var p app.Pause
		var at string
		if err := rows.Scan(&p.Scope, &p.Reason, &p.PausedBy, &at); err != nil {
			return nil, fmt.Errorf("pauses: %w", err)
		}
		if p.PausedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
