package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

const flowCols = `ticket_id, project_id, pipeline, pipeline_version, definition, stage, iteration, status, waiting, run_id,
	outcome, report, started_at, updated_at, version`

// SaveFlow stores a flow transition atomically: the flow (inserted when
// expectedVersion is 0, else updated if still at it), the ticket update
// (version-guarded), the jobs it causes and its events.
func (s *Store) SaveFlow(ctx context.Context, f app.Flow, expectedVersion int64, ticket *app.ItemWrite, jobs []app.Job, events []event.Event) error {
	def, err := json.Marshal(f.Definition)
	if err != nil {
		return fmt.Errorf("save flow: %w", err)
	}
	var stmts []sqlstore.Stmt
	if expectedVersion == 0 {
		stmts = append(stmts, sqlstore.Exec(`INSERT INTO flows (`+flowCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.TicketID, f.ProjectID, f.Pipeline, f.PipelineVersion, string(def), f.Stage, f.Iteration, string(f.Status),
			f.Waiting, f.RunID, f.Outcome, f.Report, formatTime(f.StartedAt), formatTime(f.UpdatedAt), f.Version))
	} else {
		stmts = append(stmts, sqlstore.ExecOne(`UPDATE flows SET pipeline = ?, pipeline_version = ?, definition = ?, stage = ?,
				iteration = ?, status = ?, waiting = ?, run_id = ?, outcome = ?, report = ?, started_at = ?, updated_at = ?,
				version = ? WHERE ticket_id = ? AND version = ?`,
			f.Pipeline, f.PipelineVersion, string(def), f.Stage, f.Iteration, string(f.Status), f.Waiting, f.RunID, f.Outcome,
			f.Report, formatTime(f.StartedAt), formatTime(f.UpdatedAt), f.Version, f.TicketID, expectedVersion))
	}
	if ticket != nil {
		update, err := updateItemStmt(ticket.Item, ticket.ExpectedVersion)
		if err != nil {
			return fmt.Errorf("save flow: %w", err)
		}
		stmts = append(stmts, update)
	}
	for _, j := range jobs {
		stmts = append(stmts, enqueueJobStmt(j))
	}
	for _, e := range events {
		stmts = append(stmts, s.AppendEvent(e))
	}
	return mapWriteErr("save flow", s.db.Batch(ctx, stmts...))
}

// Flow returns a ticket's flow.
func (s *Store) Flow(ctx context.Context, ticketID string) (app.Flow, error) {
	f, err := scanFlow(s.db.QueryRow(ctx, `SELECT `+flowCols+` FROM flows WHERE ticket_id = ?`, ticketID))
	return f, mapReadErr("flow", err)
}

// ActiveFlows returns flows running or waiting.
func (s *Store) ActiveFlows(ctx context.Context) ([]app.Flow, error) {
	rows, err := s.db.Query(ctx, `SELECT `+flowCols+` FROM flows WHERE status IN ('running', 'waiting') ORDER BY started_at`)
	if err != nil {
		return nil, fmt.Errorf("active flows: %w", err)
	}
	defer rows.Close()
	var out []app.Flow
	for rows.Next() {
		f, err := scanFlow(rows)
		if err != nil {
			return nil, fmt.Errorf("active flows: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanFlow(r scanner) (app.Flow, error) {
	var f app.Flow
	var def, status, started, updated string
	if err := r.Scan(&f.TicketID, &f.ProjectID, &f.Pipeline, &f.PipelineVersion, &def, &f.Stage, &f.Iteration, &status,
		&f.Waiting, &f.RunID, &f.Outcome, &f.Report, &started, &updated, &f.Version); err != nil {
		return app.Flow{}, err
	}
	f.Status = app.FlowStatus(status)
	if err := json.Unmarshal([]byte(def), &f.Definition); err != nil {
		return app.Flow{}, err
	}
	var err error
	if f.StartedAt, err = parseTime(started); err != nil {
		return app.Flow{}, err
	}
	f.UpdatedAt, err = parseTime(updated)
	return f, err
}
