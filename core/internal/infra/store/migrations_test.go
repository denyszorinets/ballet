package store

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// migratedTo returns a database at path migrated up to and including the
// migration numbered last, as an installation of that version left it.
func migratedTo(t *testing.T, path string, last string) *sqlstore.DB {
	t.Helper()
	all, err := fs.ReadDir(migrations, "migrations")
	require.NoError(t, err)
	sub := fstest.MapFS{}
	for _, e := range all {
		if e.Name()[:4] > last {
			continue
		}
		b, err := fs.ReadFile(migrations, "migrations/"+e.Name())
		require.NoError(t, err)
		sub[e.Name()] = &fstest.MapFile{Data: b}
	}
	db, err := sqlstore.Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(t.Context(), sub))
	return db
}

func exec(t *testing.T, db *sqlstore.DB, query string, args ...any) {
	t.Helper()
	require.NoError(t, db.Batch(t.Context(), sqlstore.Exec(query, args...)))
}

func scalar(t *testing.T, db *sqlstore.DB, query string, args ...any) string {
	t.Helper()
	var s string
	require.NoError(t, db.QueryRow(t.Context(), query, args...).Scan(&s))
	return s
}

func TestMigrations_PlatformScopeRewritesInstallationWideData(t *testing.T) {
	path := t.TempDir() + "/core.db"
	db := migratedTo(t, path, "0029")
	const now = "2026-10-01T00:00:00Z"
	exec(t, db, `INSERT INTO role_bindings (id, claim, value, role, scope_kind, created_at)
		VALUES ('b1', 'groups', 'ops', 'org-admin', 'organization', ?)`, now)
	exec(t, db, `INSERT INTO role_bindings (id, claim, value, role, scope_kind, customer_key, created_at)
		VALUES ('b2', 'groups', 'devs', 'engineer', 'customer', 'acme', ?)`, now)
	exec(t, db, `INSERT INTO skills (id, scope_kind, name, description, body, latest_version, created_at, updated_at, version)
		VALUES ('s1', 'organization', 'tdd', 'd', 'b', 1, ?, ?, 1)`, now, now)
	exec(t, db, `INSERT INTO skill_versions (skill_id, number, description, body, files, published_by, published_at)
		VALUES ('s1', 1, 'd', 'b', '{}', 'alice', ?)`, now)
	exec(t, db, `INSERT INTO pauses (scope, paused_by, paused_at) VALUES ('org', 'alice', ?)`, now)
	exec(t, db, `INSERT INTO events (id, occurred_at, entity_type, entity_id, type, actor_kind, actor_sub)
		VALUES ('e1', ?, 'organization', 'org', 'control.paused', 'human', 'alice')`, now)
	require.NoError(t, db.Close())

	s, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer s.Close()
	db = s.DB()

	assert.Equal(t, "platform-admin platform", scalar(t, db, `SELECT role || ' ' || scope_kind FROM role_bindings WHERE id = 'b1'`))
	assert.Equal(t, "engineer", scalar(t, db, `SELECT role FROM role_bindings WHERE id = 'b2'`), "other bindings are kept")
	assert.Equal(t, "platform", scalar(t, db, `SELECT scope_kind FROM skills WHERE id = 's1'`))
	assert.Equal(t, "1", scalar(t, db, `SELECT count(*) FROM skill_versions WHERE skill_id = 's1'`), "versions keep their skill")
	assert.Equal(t, "platform", scalar(t, db, `SELECT scope FROM pauses`))
	assert.Equal(t, "platform platform", scalar(t, db, `SELECT entity_type || ' ' || entity_id FROM events WHERE id = 'e1'`))
	assert.Equal(t, "0", scalar(t, db, `SELECT count(*) FROM pragma_foreign_key_check`))
}
