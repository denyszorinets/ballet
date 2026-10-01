// Package sqlstore is the persistence foundation of Ballet services: an
// SQLite database (modernc.org/sqlite, pure Go) used through atomic statement
// batches and embedded SQL migrations (ADR-0016, ADR-0019).
//
// Writes never use interactive transactions. A Batch is a list of statements
// executed atomically with no application logic between them, which maps
// one-to-one onto rqlite's transactional batch requests. Optimistic
// concurrency is expressed in SQL: a statement added with ExecOne must change
// exactly one row, otherwise the whole batch is rolled back and ErrConflict is
// returned.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrConflict reports that a guarded statement (ExecOne) did not change
// exactly one row — typically because the expected version was stale. The
// batch was rolled back; re-read and retry.
var ErrConflict = errors.New("sqlstore: conflicting concurrent update")

// conflictConstraint names the CHECK constraint that turns a failed guard
// into a batch-aborting error.
const conflictConstraint = "ballet_conflict"

const guardTable = `CREATE TABLE IF NOT EXISTS ballet_guard (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	ok INTEGER NOT NULL CONSTRAINT ` + conflictConstraint + ` CHECK (ok = 1)
)`

// guardSQL records whether the previous statement changed exactly one row.
// The CHECK constraint fails otherwise, aborting the batch (REPLACE behaves
// like ABORT for CHECK violations).
const guardSQL = `INSERT OR REPLACE INTO ballet_guard (id, ok) VALUES (1, changes() = 1)`

// Stmt is one statement of a batch.
type Stmt struct {
	SQL  string
	Args []any
	one  bool
}

// Exec returns a statement for a batch.
func Exec(query string, args ...any) Stmt {
	return Stmt{SQL: query, Args: args}
}

// ExecOne returns a statement that must change exactly one row, e.g. an
// UPDATE guarded by "WHERE id = ? AND version = ?".
func ExecOne(query string, args ...any) Stmt {
	return Stmt{SQL: query, Args: args, one: true}
}

// DB is an open service database.
type DB struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database file at path with WAL
// journaling, foreign keys and a busy timeout, and registers Ballet's SQL
// functions.
func Open(ctx context.Context, path string) (*DB, error) {
	registerFunctions()

	q := url.Values{}
	q.Add("_pragma", "journal_mode(wal)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(normal)")
	q.Set("_txlock", "immediate") // writers take the lock up front: no upgrade deadlocks
	sqlDB, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := sqlDB.ExecContext(ctx, guardTable); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &DB{db: sqlDB}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// Ping checks that the database is usable; suitable as a readiness check.
func (d *DB) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// Query runs a read query.
func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

// QueryRow runs a read query returning at most one row.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

// Batch executes stmts atomically: either all take effect or none do. It
// returns ErrConflict when an ExecOne statement did not change exactly one
// row.
func (d *DB) Batch(ctx context.Context, stmts ...Stmt) error {
	if len(stmts) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	for i, s := range stmts {
		if _, err := tx.ExecContext(ctx, s.SQL, s.Args...); err != nil {
			return fmt.Errorf("batch statement %d: %w", i, err)
		}
		if s.one {
			if _, err := tx.ExecContext(ctx, guardSQL); err != nil {
				if strings.Contains(err.Error(), conflictConstraint) {
					return ErrConflict
				}
				return fmt.Errorf("batch statement %d guard: %w", i, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}
	return nil
}
