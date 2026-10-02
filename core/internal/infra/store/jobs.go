package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

const jobCols = `id, kind, dedupe_key, payload, status, run_at, attempts, max_attempts, lease_until, last_error,
	created_at, updated_at, version`

// jobTime formats times that are compared as strings (run_at,
// lease_until): fixed width, so the order of strings is the order of times.
func jobTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func nullableJobTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return jobTime(t)
}

// enqueueJobStmt inserts a job unless its dedupe key is live.
func enqueueJobStmt(j app.Job) sqlstore.Stmt {
	return sqlstore.Exec(`INSERT OR IGNORE INTO jobs (`+jobCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Kind, j.DedupeKey, string(j.Payload), string(j.Status), jobTime(j.RunAt), j.Attempts, j.MaxAttempts,
		nullableJobTime(j.LeaseUntil), j.LastError, formatTime(j.CreatedAt), formatTime(j.UpdatedAt), j.Version)
}

// EnqueueJobs inserts jobs, skipping those whose dedupe key is live.
func (s *Store) EnqueueJobs(ctx context.Context, jobs ...app.Job) error {
	stmts := make([]sqlstore.Stmt, 0, len(jobs))
	for _, j := range jobs {
		stmts = append(stmts, enqueueJobStmt(j))
	}
	return mapWriteErr("enqueue jobs", s.db.Batch(ctx, stmts...))
}

// DueJobs returns pending jobs due by now and running jobs whose lease
// expired, oldest first.
func (s *Store) DueJobs(ctx context.Context, now time.Time, limit int) ([]app.Job, error) {
	t := jobTime(now)
	rows, err := s.db.Query(ctx, `SELECT `+jobCols+` FROM jobs
		WHERE (status = 'pending' AND run_at <= ?) OR (status = 'running' AND lease_until <= ?)
		ORDER BY run_at, id LIMIT ?`, t, t, limit)
	if err != nil {
		return nil, fmt.Errorf("due jobs: %w", err)
	}
	defer rows.Close()
	var out []app.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("due jobs: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// UpdateJob stores j if it is at expectedVersion.
func (s *Store) UpdateJob(ctx context.Context, j app.Job, expectedVersion int64) error {
	return mapWriteErr("update job", s.db.Batch(ctx, sqlstore.ExecOne(`UPDATE jobs SET status = ?, run_at = ?,
			attempts = ?, lease_until = ?, last_error = ?, updated_at = ?, version = ? WHERE id = ? AND version = ?`,
		string(j.Status), jobTime(j.RunAt), j.Attempts, nullableJobTime(j.LeaseUntil), j.LastError, formatTime(j.UpdatedAt),
		j.Version, j.ID, expectedVersion)))
}

// Job returns the job with id.
func (s *Store) Job(ctx context.Context, id string) (app.Job, error) {
	j, err := scanJob(s.db.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	return j, mapReadErr("job "+id, err)
}

func scanJob(r scanner) (app.Job, error) {
	var j app.Job
	var payload, status, runAt, created, updated string
	var lease sql.NullString
	if err := r.Scan(&j.ID, &j.Kind, &j.DedupeKey, &payload, &status, &runAt, &j.Attempts, &j.MaxAttempts, &lease,
		&j.LastError, &created, &updated, &j.Version); err != nil {
		return app.Job{}, err
	}
	j.Payload, j.Status = []byte(payload), app.JobStatus(status)
	var err error
	if j.RunAt, err = parseTime(runAt); err != nil {
		return app.Job{}, err
	}
	if lease.Valid {
		if j.LeaseUntil, err = parseTime(lease.String); err != nil {
			return app.Job{}, err
		}
	}
	if j.CreatedAt, err = parseTime(created); err != nil {
		return app.Job{}, err
	}
	j.UpdatedAt, err = parseTime(updated)
	return j, err
}
