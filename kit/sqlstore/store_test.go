package sqlstore_test

import (
	"errors"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/sqlstore"
	"github.com/denyszorinets/ballet/kit/sqlstore/sqlstoretest"
)

var itemsMigrations = fstest.MapFS{
	"0001_items.sql": {Data: []byte(`
CREATE TABLE items (id TEXT PRIMARY KEY, name TEXT NOT NULL, version INTEGER NOT NULL);
CREATE TABLE item_events (id INTEGER PRIMARY KEY, item_id TEXT NOT NULL, kind TEXT NOT NULL);`)},
	"0002_items_note.sql": {Data: []byte(`ALTER TABLE items ADD COLUMN note TEXT NOT NULL DEFAULT '';`)},
}

func countRows(t *testing.T, db *sqlstore.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func TestMigrate_AppliesMigrationsInOrderOnce(t *testing.T) {
	db := sqlstoretest.New(t, itemsMigrations)

	// Migrations already applied by sqlstoretest.New; applying again is a no-op.
	require.NoError(t, db.Migrate(t.Context(), itemsMigrations))

	var versions []int
	rows, err := db.Query(t.Context(), "SELECT version FROM schema_migrations ORDER BY version")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var v int
		require.NoError(t, rows.Scan(&v))
		versions = append(versions, v)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []int{1, 2}, versions)
	assert.Equal(t, 0, countRows(t, db, "items"))
}

func TestMigrate_RejectsBadFileNames(t *testing.T) {
	db := sqlstoretest.New(t, nil)

	err := db.Migrate(t.Context(), fstest.MapFS{"init.sql": {Data: []byte("SELECT 1;")}})

	assert.ErrorContains(t, err, "init.sql")
}

func TestMigrate_FailedMigrationLeavesNoTrace(t *testing.T) {
	db := sqlstoretest.New(t, nil)

	err := db.Migrate(t.Context(), fstest.MapFS{
		"0001_broken.sql": {Data: []byte("CREATE TABLE ok_table (id INTEGER); CREATE TABLE broken (;")},
	})

	require.Error(t, err)
	var n int
	require.NoError(t, db.QueryRow(t.Context(),
		"SELECT count(*) FROM sqlite_master WHERE name = 'ok_table'").Scan(&n))
	assert.Equal(t, 0, n, "partial migration must be rolled back")
}

func TestBatch_IsAtomic(t *testing.T) {
	db := sqlstoretest.New(t, itemsMigrations)

	err := db.Batch(t.Context(),
		sqlstore.Exec("INSERT INTO items (id, name, version) VALUES (?, ?, 1)", "a", "first"),
		sqlstore.Exec("INSERT INTO items (id, name, version) VALUES (?, ?, 1)", "a", "duplicate key"),
	)

	require.Error(t, err)
	assert.False(t, errors.Is(err, sqlstore.ErrConflict))
	assert.Equal(t, 0, countRows(t, db, "items"))
}

func TestBatch_GuardedStatementConflictRollsBackWholeBatch(t *testing.T) {
	db := sqlstoretest.New(t, itemsMigrations)
	require.NoError(t, db.Batch(t.Context(),
		sqlstore.Exec("INSERT INTO items (id, name, version) VALUES ('a', 'first', 1)")))

	err := db.Batch(t.Context(),
		sqlstore.ExecOne("UPDATE items SET name = 'second', version = 2 WHERE id = 'a' AND version = ?", 7),
		sqlstore.Exec("INSERT INTO item_events (item_id, kind) VALUES ('a', 'renamed')"),
	)

	require.ErrorIs(t, err, sqlstore.ErrConflict)
	var name string
	require.NoError(t, db.QueryRow(t.Context(), "SELECT name FROM items WHERE id = 'a'").Scan(&name))
	assert.Equal(t, "first", name)
	assert.Equal(t, 0, countRows(t, db, "item_events"))
}

func TestBatch_GuardedStatementSucceedsWhenVersionMatches(t *testing.T) {
	db := sqlstoretest.New(t, itemsMigrations)
	require.NoError(t, db.Batch(t.Context(),
		sqlstore.Exec("INSERT INTO items (id, name, version) VALUES ('a', 'first', 1)")))

	err := db.Batch(t.Context(),
		sqlstore.ExecOne("UPDATE items SET name = 'second', version = 2 WHERE id = 'a' AND version = ?", 1),
		sqlstore.Exec("INSERT INTO item_events (item_id, kind) VALUES ('a', 'renamed')"),
	)

	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, db, "item_events"))
}

func TestBatch_ConcurrentUpdatesOfSameVersionYieldOneWinner(t *testing.T) {
	db := sqlstoretest.New(t, itemsMigrations)
	require.NoError(t, db.Batch(t.Context(),
		sqlstore.Exec("INSERT INTO items (id, name, version) VALUES ('a', 'first', 1)")))

	const writers = 8
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Go(func() {
			errs[i] = db.Batch(t.Context(),
				sqlstore.ExecOne("UPDATE items SET version = 2, name = ? WHERE id = 'a' AND version = 1", i),
				sqlstore.Exec("INSERT INTO item_events (item_id, kind) VALUES ('a', 'renamed')"),
			)
		})
	}
	wg.Wait()

	var ok, conflicts int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, sqlstore.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, ok)
	assert.Equal(t, writers-1, conflicts)
	assert.Equal(t, 1, countRows(t, db, "item_events"))
}

func TestBatch_EmptyBatchIsNoOp(t *testing.T) {
	db := sqlstoretest.New(t, nil)

	assert.NoError(t, db.Batch(t.Context()))
}
