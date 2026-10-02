package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// Budget returns the budget of scope; a zero Budget (version 0) when none
// is set.
func (s *Store) Budget(ctx context.Context, scope string) (app.Budget, error) {
	b := app.Budget{Scope: scope}
	var updated string
	err := s.db.QueryRow(ctx, `SELECT ticket_tokens, daily_tokens, updated_by, updated_at, version FROM budgets
		WHERE scope = ?`, scope).Scan(&b.TicketTokens, &b.DailyTokens, &b.UpdatedBy, &updated, &b.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return app.Budget{}, fmt.Errorf("budget: %w", err)
	}
	b.UpdatedAt, err = parseTime(updated)
	return b, err
}

// SetBudget stores b if the stored budget is at expectedVersion (0: none
// yet), and records e.
func (s *Store) SetBudget(ctx context.Context, b app.Budget, expectedVersion int64, e event.Event) error {
	stmt := sqlstore.ExecOne(`INSERT INTO budgets (scope, ticket_tokens, daily_tokens, updated_by, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?)`, b.Scope, b.TicketTokens, b.DailyTokens, b.UpdatedBy, formatTime(b.UpdatedAt), b.Version)
	if expectedVersion > 0 {
		stmt = sqlstore.ExecOne(`UPDATE budgets SET ticket_tokens = ?, daily_tokens = ?, updated_by = ?, updated_at = ?,
			version = ? WHERE scope = ? AND version = ?`, b.TicketTokens, b.DailyTokens, b.UpdatedBy,
			formatTime(b.UpdatedAt), b.Version, b.Scope, expectedVersion)
	}
	err := mapWriteErr("set budget", s.db.Batch(ctx, stmt, s.AppendEvent(e)))
	if errors.Is(err, app.ErrAlreadyExists) {
		return fmt.Errorf("set budget: %w", app.ErrConflict)
	}
	return err
}

// CountedTokens sums the tokens budgets count (input, output and cache
// writes; cache reads are not counted) of usage matching f.
func (s *Store) CountedTokens(ctx context.Context, f app.UsageFilter) (int64, error) {
	var n sql.NullInt64
	until := ""
	if !f.Until.IsZero() {
		until = formatTime(f.Until)
	}
	err := s.db.QueryRow(ctx, `SELECT sum(input_tokens + output_tokens + cache_write_tokens) FROM usage_records
		WHERE (? = '' OR customer_id = ?) AND (? = '' OR project_id = ?) AND (? = '' OR ticket_key = ?)
		AND occurred_at >= ? AND (? = '' OR occurred_at < ?)`,
		f.CustomerID, f.CustomerID, f.ProjectID, f.ProjectID, f.Ticket, f.Ticket, formatTime(f.Since), until, until).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counted tokens: %w", err)
	}
	return n.Int64, nil
}
