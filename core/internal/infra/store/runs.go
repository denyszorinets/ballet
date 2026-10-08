package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

const runCols = `id, project_id, ticket_id, stage, status, spec, agent, exit_code, error, created_by, created_at,
	started_at, finished_at, version, branch, adapter, result`

// CreateRun inserts a queued run and records e.
func (s *Store) CreateRun(ctx context.Context, r run.Run, e event.Event) error {
	spec, err := json.Marshal(r.Spec)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return mapWriteErr("create run", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO runs (`+runCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.ProjectID, r.TicketID, r.Stage, string(r.Status), string(spec), r.Agent, nullableInt(r.ExitCode),
			r.Error, r.CreatedBy, formatTime(r.CreatedAt), nullableTime(r.StartedAt), nullableTime(r.FinishedAt), r.Version,
			r.Branch, r.Adapter, resultJSON(r.Result)),
		s.AppendEvent(e),
	))
}

// UpdateRun stores r if the stored run is at expectedVersion, recording e
// when it is not nil.
func (s *Store) UpdateRun(ctx context.Context, r run.Run, expectedVersion int64, e *event.Event) error {
	stmts := []sqlstore.Stmt{sqlstore.ExecOne(`UPDATE runs SET status = ?, agent = ?, exit_code = ?, error = ?,
			started_at = ?, finished_at = ?, result = ?, version = ? WHERE id = ? AND version = ?`,
		string(r.Status), r.Agent, nullableInt(r.ExitCode), r.Error, nullableTime(r.StartedAt), nullableTime(r.FinishedAt),
		resultJSON(r.Result), r.Version, r.ID, expectedVersion)}
	if e != nil {
		stmts = append(stmts, s.AppendEvent(*e))
	}
	return mapWriteErr("update run", s.db.Batch(ctx, stmts...))
}

// Run returns the run with id.
func (s *Store) Run(ctx context.Context, id string) (run.Run, error) {
	r, err := scanRun(s.db.QueryRow(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	return r, mapReadErr("run "+id, err)
}

// ListRuns returns runs matching f, oldest first.
func (s *Store) ListRuns(ctx context.Context, f app.RunFilter) ([]run.Run, error) {
	q := `SELECT ` + runCols + ` FROM runs WHERE 1 = 1`
	var args []any
	if f.TicketID != "" {
		q, args = q+` AND ticket_id = ?`, append(args, f.TicketID)
	}
	if f.Agent != "" {
		q, args = q+` AND agent = ?`, append(args, f.Agent)
	}
	if len(f.Statuses) > 0 {
		q += ` AND status IN (?` + strings.Repeat(", ?", len(f.Statuses)-1) + `)`
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY created_at, id`
	if f.Limit > 0 {
		q, args = q+` LIMIT ?`, append(args, f.Limit)
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	var out []run.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("list runs: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AppendRunLog appends a chunk of output unless the run's log would exceed
// maxBytes; it reports whether the chunk was stored.
func (s *Store) AppendRunLog(ctx context.Context, runID, stream, text string, at time.Time, maxBytes int64) (bool, error) {
	err := s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE runs SET log_seq = log_seq + 1, log_bytes = log_bytes + ?
			WHERE id = ? AND log_bytes + ? <= ?`, len(text), runID, len(text), maxBytes),
		sqlstore.Exec(`INSERT INTO run_logs (run_id, seq, stream, text, at)
			SELECT id, log_seq - 1, ?, ?, ? FROM runs WHERE id = ?`, stream, text, formatTime(at), runID),
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sqlstore.ErrConflict) {
		return false, nil // the log is full
	}
	return false, mapWriteErr("append run log", err)
}

// SaveRunState keeps what continues a parked run's session.
func (s *Store) SaveRunState(ctx context.Context, runID string, data []byte) error {
	return mapWriteErr("save run state", s.db.Batch(ctx, sqlstore.Exec(`INSERT INTO run_states (run_id, data) VALUES (?, ?)
		ON CONFLICT (run_id) DO UPDATE SET data = excluded.data`, runID, base64.StdEncoding.EncodeToString(data))))
}

// RunState returns what continues a parked run's session.
func (s *Store) RunState(ctx context.Context, runID string) ([]byte, error) {
	var data string
	if err := s.db.QueryRow(ctx, `SELECT data FROM run_states WHERE run_id = ?`, runID).Scan(&data); err != nil {
		return nil, mapReadErr("run state", err)
	}
	return base64.StdEncoding.DecodeString(data)
}

// RunLogs returns up to limit chunks of a run's output after seq.
func (s *Store) RunLogs(ctx context.Context, runID string, afterSeq int64, limit int) ([]app.RunLog, error) {
	rows, err := s.db.Query(ctx, `SELECT seq, stream, text, at FROM run_logs WHERE run_id = ? AND seq > ?
		ORDER BY seq LIMIT ?`, runID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("run logs: %w", err)
	}
	defer rows.Close()
	var out []app.RunLog
	for rows.Next() {
		var l app.RunLog
		var at string
		if err := rows.Scan(&l.Seq, &l.Stream, &l.Text, &at); err != nil {
			return nil, fmt.Errorf("run logs: %w", err)
		}
		if l.At, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("run logs: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func scanRun(r scanner) (run.Run, error) {
	var x run.Run
	var status, spec, created string
	var exit sql.NullInt64
	var started, finished sql.NullString
	var result string
	if err := r.Scan(&x.ID, &x.ProjectID, &x.TicketID, &x.Stage, &status, &spec, &x.Agent, &exit, &x.Error,
		&x.CreatedBy, &created, &started, &finished, &x.Version, &x.Branch, &x.Adapter, &result); err != nil {
		return run.Run{}, err
	}
	if result != "" {
		x.Result = &run.Result{}
		if err := json.Unmarshal([]byte(result), x.Result); err != nil {
			return run.Run{}, err
		}
	}
	x.Status = run.Status(status)
	if err := json.Unmarshal([]byte(spec), &x.Spec); err != nil {
		return run.Run{}, err
	}
	if exit.Valid {
		code := int(exit.Int64)
		x.ExitCode = &code
	}
	var err error
	if x.CreatedAt, err = parseTime(created); err != nil {
		return run.Run{}, err
	}
	if started.Valid {
		if x.StartedAt, err = parseTime(started.String); err != nil {
			return run.Run{}, err
		}
	}
	if finished.Valid {
		if x.FinishedAt, err = parseTime(finished.String); err != nil {
			return run.Run{}, err
		}
	}
	return x, nil
}

func resultJSON(r *run.Result) string {
	if r == nil {
		return ""
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}
