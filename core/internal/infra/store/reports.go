package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// CreateReport stores a run's report and records e.
func (s *Store) CreateReport(ctx context.Context, r report.Report, e event.Event) error {
	return mapWriteErr("create report", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO agent_reports (id, project_id, ticket_id, run_id, kind, outcome, text, detail, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.ProjectID, r.TicketID, r.RunID, string(r.Kind), string(r.Outcome),
			r.Text, r.Detail, formatTime(r.CreatedAt)),
		s.AppendEvent(e),
	))
}

// Reports returns a ticket's reports, oldest first; runID narrows them to
// one run when set.
func (s *Store) Reports(ctx context.Context, ticketID, runID string) ([]report.Report, error) {
	rows, err := s.db.Query(ctx, `SELECT id, project_id, ticket_id, run_id, kind, outcome, text, detail, created_at
		FROM agent_reports WHERE ticket_id = ? AND (? = '' OR run_id = ?) ORDER BY created_at, id`, ticketID, runID, runID)
	if err != nil {
		return nil, fmt.Errorf("reports: %w", err)
	}
	defer rows.Close()
	var out []report.Report
	for rows.Next() {
		var r report.Report
		var kind, outcome, created string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.TicketID, &r.RunID, &kind, &outcome, &r.Text, &r.Detail, &created); err != nil {
			return nil, fmt.Errorf("reports: %w", err)
		}
		r.Kind, r.Outcome = report.Kind(kind), report.Outcome(outcome)
		if r.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateQuestion stores a question and records e.
func (s *Store) CreateQuestion(ctx context.Context, q report.Question, e event.Event) error {
	return mapWriteErr("create question", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO questions (id, project_id, ticket_id, run_id, text, context, blocking, status, route, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, q.ID, q.ProjectID, q.TicketID, q.RunID, q.Text, q.Context, q.Blocking,
			string(q.Status), q.Route, formatTime(q.CreatedAt)),
		s.AppendEvent(e),
	))
}

const questionCols = `id, project_id, ticket_id, run_id, text, context, blocking, status, route, answer, answered_by,
	created_at, answered_at`

// Question returns a question by ID.
func (s *Store) Question(ctx context.Context, id string) (report.Question, error) {
	q, err := scanQuestion(s.db.QueryRow(ctx, `SELECT `+questionCols+` FROM questions WHERE id = ?`, id))
	if err != nil {
		return report.Question{}, mapReadErr("question", err)
	}
	return q, nil
}

// Questions returns a ticket's questions, oldest first.
func (s *Store) Questions(ctx context.Context, ticketID string) ([]report.Question, error) {
	rows, err := s.db.Query(ctx, `SELECT `+questionCols+` FROM questions WHERE ticket_id = ? ORDER BY created_at, id`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("questions: %w", err)
	}
	defer rows.Close()
	var out []report.Question
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, fmt.Errorf("questions: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// RouteQuestion sets an open question's route and records e.
func (s *Store) RouteQuestion(ctx context.Context, id, route string, e event.Event) error {
	return mapWriteErr("route question", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE questions SET route = ? WHERE id = ? AND status = 'open'`, route, id),
		s.AppendEvent(e),
	))
}

// AnswerQuestion records the answer of an open question (ErrConflict when
// it was answered meanwhile), the jobs it causes and e, atomically.
func (s *Store) AnswerQuestion(ctx context.Context, q report.Question, jobs []app.Job, e event.Event) error {
	stmts := []sqlstore.Stmt{
		sqlstore.ExecOne(`UPDATE questions SET status = 'answered', answer = ?, answered_by = ?, answered_at = ?
			WHERE id = ? AND status = 'open'`, q.Answer, q.AnsweredBy, formatTime(q.AnsweredAt), q.ID),
	}
	for _, j := range jobs {
		stmts = append(stmts, enqueueJobStmt(j))
	}
	stmts = append(stmts, s.AppendEvent(e))
	return mapWriteErr("answer question", s.db.Batch(ctx, stmts...))
}

func scanQuestion(r scanner) (report.Question, error) {
	var q report.Question
	var status, created string
	var answered sql.NullString
	if err := r.Scan(&q.ID, &q.ProjectID, &q.TicketID, &q.RunID, &q.Text, &q.Context, &q.Blocking, &status, &q.Route,
		&q.Answer, &q.AnsweredBy, &created, &answered); err != nil {
		return report.Question{}, err
	}
	q.Status = report.QuestionStatus(status)
	var err error
	if q.CreatedAt, err = parseTime(created); err != nil {
		return report.Question{}, err
	}
	if answered.Valid {
		if q.AnsweredAt, err = parseTime(answered.String); err != nil {
			return report.Question{}, err
		}
	}
	return q, nil
}

// OpenQuestions returns every open question, oldest first.
func (s *Store) OpenQuestions(ctx context.Context) ([]report.Question, error) {
	rows, err := s.db.Query(ctx, `SELECT `+questionCols+` FROM questions WHERE status = 'open' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("open questions: %w", err)
	}
	defer rows.Close()
	var out []report.Question
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, fmt.Errorf("open questions: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
