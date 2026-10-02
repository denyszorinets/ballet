package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

const prCols = `ticket_id, project_id, forge, number, url, title, head, base, head_sha, state, draft, mergeable, checks,
	review, updated_at`

// SavePullRequest stores a ticket's pull request (insert or replace) and
// records e when it is not nil.
func (s *Store) SavePullRequest(ctx context.Context, p app.TicketPR, e *event.Event) error {
	var mergeable any
	if p.Mergeable != nil {
		mergeable = *p.Mergeable
	}
	stmts := []sqlstore.Stmt{sqlstore.Exec(`INSERT OR REPLACE INTO pull_requests (`+prCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.TicketID, p.ProjectID, p.Forge, p.Number, p.URL, p.Title, p.Head, p.Base, p.HeadSHA, string(p.State), p.Draft,
		mergeable, string(p.Checks), string(p.Review), formatTime(p.UpdatedAt))}
	if e != nil {
		stmts = append(stmts, s.AppendEvent(*e))
	}
	return mapWriteErr("save pull request", s.db.Batch(ctx, stmts...))
}

// PullRequest returns a ticket's pull request.
func (s *Store) PullRequest(ctx context.Context, ticketID string) (app.TicketPR, error) {
	p, err := scanPR(s.db.QueryRow(ctx, `SELECT `+prCols+` FROM pull_requests WHERE ticket_id = ?`, ticketID))
	return p, mapReadErr("pull request", err)
}

// OpenPullRequests returns the pull requests still open.
func (s *Store) OpenPullRequests(ctx context.Context) ([]app.TicketPR, error) {
	rows, err := s.db.Query(ctx, `SELECT `+prCols+` FROM pull_requests WHERE state = 'open' ORDER BY updated_at`)
	if err != nil {
		return nil, fmt.Errorf("open pull requests: %w", err)
	}
	defer rows.Close()
	var out []app.TicketPR
	for rows.Next() {
		p, err := scanPR(rows)
		if err != nil {
			return nil, fmt.Errorf("open pull requests: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPR(r scanner) (app.TicketPR, error) {
	var p app.TicketPR
	var state, checks, review, updated string
	var mergeable sql.NullBool
	if err := r.Scan(&p.TicketID, &p.ProjectID, &p.Forge, &p.Number, &p.URL, &p.Title, &p.Head, &p.Base, &p.HeadSHA,
		&state, &p.Draft, &mergeable, &checks, &review, &updated); err != nil {
		return app.TicketPR{}, err
	}
	p.State, p.Checks, p.Review = forge.State(state), forge.Checks(checks), forge.Review(review)
	if mergeable.Valid {
		m := mergeable.Bool
		p.Mergeable = &m
	}
	var err error
	p.UpdatedAt, err = parseTime(updated)
	return p, err
}
