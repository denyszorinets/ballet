// Package store is Core's SQLite persistence: migrations and repositories
// for all Core entities, built on kit/sqlstore (atomic batches, optimistic
// concurrency). Every mutation is a batch that includes its event.
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/google/uuid"

	"github.com/denyszorinets/ballet/kit/sqlstore"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is Core's database.
type Store struct {
	db  *sqlstore.DB
	now func() time.Time
}

// Open opens the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlstore.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Migrate(ctx, sub); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate core database: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying database for batches and readiness checks.
func (s *Store) DB() *sqlstore.DB { return s.db }

// NewID returns a new time-ordered unique identifier (UUIDv7).
func NewID() string {
	return uuid.Must(uuid.NewV7()).String()
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }
