package sqlstore

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
)

const migrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version int
	name    string
}

// Migrate applies the migrations in fsys that have not been applied yet, in
// version order. Migration files live at the root of fsys and are named
// NNNN_description.sql (e.g. 0001_tickets.sql). Each migration is applied
// atomically together with its schema_migrations record. Applied migrations
// must never be edited; add a new one instead.
func (d *DB) Migrate(ctx context.Context, fsys fs.FS) error {
	if _, err := d.db.ExecContext(ctx, migrationsTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	pending, err := d.pendingMigrations(ctx, fsys)
	if err != nil {
		return err
	}
	for _, m := range pending {
		script, err := fs.ReadFile(fsys, m.name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", m.name, err)
		}
		if err := d.Batch(ctx,
			Exec(string(script)),
			Exec("INSERT INTO schema_migrations (version, name) VALUES (?, ?)", m.version, m.name),
		); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
	}
	return nil
}

func (d *DB) pendingMigrations(ctx context.Context, fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var all []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		match := migrationName.FindStringSubmatch(e.Name())
		if match == nil {
			return nil, fmt.Errorf("migration file %q: name must match NNNN_description.sql", e.Name())
		}
		v, _ := strconv.Atoi(match[1]) // four digits always parse
		all = append(all, migration{version: v, name: e.Name()})
	}
	slices.SortFunc(all, func(a, b migration) int { return a.version - b.version })
	for i := 1; i < len(all); i++ {
		if all[i].version == all[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %04d", all[i].version)
		}
	}

	applied := map[int]bool{}
	rows, err := d.db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("read applied migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}

	var pending []migration
	for _, m := range all {
		if !applied[m.version] {
			pending = append(pending, m)
		}
	}
	return pending, nil
}
