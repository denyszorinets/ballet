package store

import (
	"context"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// InsertUsage stores usage records in one batch.
func (s *Store) InsertUsage(ctx context.Context, records []app.UsageRecord) error {
	stmts := make([]sqlstore.Stmt, 0, len(records))
	for _, r := range records {
		stmts = append(stmts, sqlstore.Exec(`INSERT INTO usage_records
			(occurred_at, organization_id, project_id, ticket_key, run, model, status,
			 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			formatTime(r.OccurredAt), r.OrganizationID, r.ProjectID, r.Ticket, r.Run, r.Model, r.Status,
			r.InputTokens, r.OutputTokens, r.CacheRead, r.CacheWrite))
	}
	return mapWriteErr("insert usage", s.db.Batch(ctx, stmts...))
}

// AggregateUsage sums a project's usage since a time, grouped by ticket
// or model, largest first.
func (s *Store) AggregateUsage(ctx context.Context, projectID, groupBy string, since time.Time, ticket string) ([]app.UsageTotals, error) {
	col := map[string]string{"ticket": "ticket_key", "model": "model", "run": "run"}[groupBy]
	if col == "" {
		return nil, fmt.Errorf("aggregate usage: unknown grouping %q", groupBy)
	}
	rows, err := s.db.Query(ctx, `SELECT `+col+`, count(*), sum(input_tokens), sum(output_tokens),
			sum(cache_read_tokens), sum(cache_write_tokens)
		FROM usage_records WHERE project_id = ? AND occurred_at >= ? AND (? = '' OR ticket_key = ?)
		GROUP BY `+col+` ORDER BY sum(input_tokens) + sum(output_tokens) DESC`, projectID, formatTime(since), ticket, ticket)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage: %w", err)
	}
	defer rows.Close()
	var out []app.UsageTotals
	for rows.Next() {
		var t app.UsageTotals
		if err := rows.Scan(&t.Key, &t.Requests, &t.InputTokens, &t.OutputTokens, &t.CacheRead, &t.CacheWrite); err != nil {
			return nil, fmt.Errorf("aggregate usage: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
