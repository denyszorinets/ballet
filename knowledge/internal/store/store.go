// Package store is Knowledge's SQLite persistence. Every query is scoped
// to one organization (knowledge space).
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/kit/sqlstore"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is Knowledge's database.
type Store struct {
	db *sqlstore.DB
}

// Open opens the database at path and applies migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlstore.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	sub, _ := fs.Sub(migrations, "migrations")
	if err := db.Migrate(ctx, sub); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate knowledge database: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the database (readiness checks, search indexing).
func (s *Store) DB() *sqlstore.DB { return s.db }

// Create inserts an entry and its first version.
func (s *Store) Create(ctx context.Context, e domain.Entry) error {
	projects, items := jsonList(e.Projects), jsonList(e.Items)
	return s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO entries (id, organization, kind, title, body, projects, items, version,
				created_by, updated_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.Organization, string(e.Kind), e.Title, e.Body, projects, items, e.Version,
			e.CreatedBy, e.UpdatedBy, ts(e.CreatedAt), ts(e.UpdatedAt)),
		versionStmt(e),
	)
}

// Update stores e if the stored version is expected, adding a version.
func (s *Store) Update(ctx context.Context, e domain.Entry, expected int64) error {
	err := s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE entries SET kind = ?, title = ?, body = ?, projects = ?, items = ?, version = ?,
				updated_by = ?, updated_at = ? WHERE id = ? AND organization = ? AND version = ?`,
			string(e.Kind), e.Title, e.Body, jsonList(e.Projects), jsonList(e.Items), e.Version,
			e.UpdatedBy, ts(e.UpdatedAt), e.ID, e.Organization, expected),
		versionStmt(e),
	)
	if errors.Is(err, sqlstore.ErrConflict) {
		return app.ErrConflict
	}
	return err
}

func versionStmt(e domain.Entry) sqlstore.Stmt {
	return sqlstore.Exec(`INSERT INTO entry_versions (entry_id, version, title, body, author, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, e.ID, e.Version, e.Title, e.Body, e.UpdatedBy, ts(e.UpdatedAt))
}

const cols = `id, organization, kind, title, body, projects, items, version, created_by, updated_by, created_at, updated_at`

// Get returns an entry of organization.
func (s *Store) Get(ctx context.Context, organization, id string) (domain.Entry, error) {
	e, err := scan(s.db.QueryRow(ctx, `SELECT `+cols+` FROM entries WHERE organization = ? AND id = ?`, organization, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Entry{}, fmt.Errorf("entry %s: %w", id, app.ErrNotFound)
	}
	return e, err
}

// List returns an organization's entries, most recently updated first.
func (s *Store) List(ctx context.Context, organization string, f app.Filter) ([]domain.Entry, error) {
	where, args := []string{"organization = ?"}, []any{organization}
	if f.Kind != "" {
		where, args = append(where, "kind = ?"), append(args, string(f.Kind))
	}
	if f.Project != "" {
		where, args = append(where, "EXISTS (SELECT 1 FROM json_each(projects) WHERE value = ?)"), append(args, f.Project)
	}
	if f.Item != "" {
		where, args = append(where, "EXISTS (SELECT 1 FROM json_each(items) WHERE value = ?)"), append(args, f.Item)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := s.db.Query(ctx, `SELECT `+cols+` FROM entries WHERE `+strings.Join(where, " AND ")+
		` ORDER BY updated_at DESC, id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()
	var out []domain.Entry
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Versions returns the versions of an entry of organization, newest first.
func (s *Store) Versions(ctx context.Context, organization, id string) ([]domain.Version, error) {
	if _, err := s.Get(ctx, organization, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT version, title, body, author, created_at FROM entry_versions
		WHERE entry_id = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	defer rows.Close()
	var out []domain.Version
	for rows.Next() {
		var v domain.Version
		var created string
		if err := rows.Scan(&v.Version, &v.Title, &v.Body, &v.Author, &created); err != nil {
			return nil, err
		}
		v.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, v)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (domain.Entry, error) {
	return scanWith(r)
}

func fill(e domain.Entry, kind, projects, items, created, updated string) domain.Entry {
	e.Kind = domain.Kind(kind)
	_ = json.Unmarshal([]byte(projects), &e.Projects)
	_ = json.Unmarshal([]byte(items), &e.Items)
	e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return e
}

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
