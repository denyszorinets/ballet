package store

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/sqlstore"
)

func TestMigrations_OrganizationsKeepEntriesAndEmbeddings(t *testing.T) {
	path := t.TempDir() + "/knowledge.db"
	old := fstest.MapFS{}
	for _, name := range []string{"0001_entries.sql", "0002_search.sql"} {
		b, err := fs.ReadFile(migrations, "migrations/"+name)
		require.NoError(t, err)
		old[name] = &fstest.MapFile{Data: b}
	}
	db, err := sqlstore.Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(t.Context(), old))
	const now = "2026-10-01T00:00:00Z"
	require.NoError(t, db.Batch(t.Context(),
		sqlstore.Exec(`INSERT INTO entries (id, customer, kind, title, body, version, created_by, updated_by, created_at, updated_at)
			VALUES ('k1', 'acme', 'doc', 'Auth', 'OIDC', 1, 'alice', 'alice', ?, ?)`, now, now),
		sqlstore.Exec(`INSERT INTO entry_embeddings (entry_id, customer, model, content_hash, vector, updated_at)
			VALUES ('k1', 'acme', 'm', 'h', x'00', ?)`, now),
	))
	require.NoError(t, db.Close())

	s, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer s.Close()
	var org, embeddingOrg string
	require.NoError(t, s.DB().QueryRow(t.Context(), `SELECT organization FROM entries WHERE id = 'k1'`).Scan(&org))
	require.NoError(t, s.DB().QueryRow(t.Context(), `SELECT organization FROM entry_embeddings WHERE entry_id = 'k1'`).Scan(&embeddingOrg))
	assert.Equal(t, "acme", org)
	assert.Equal(t, "acme", embeddingOrg)
}
