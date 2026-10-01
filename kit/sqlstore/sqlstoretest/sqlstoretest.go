// Package sqlstoretest provides real, temporary databases for tests.
package sqlstoretest

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// New opens a fresh database file in t.TempDir(), applies migrations (if not
// nil) and closes the database when the test ends.
func New(t testing.TB, migrations fs.FS) *sqlstore.DB {
	t.Helper()
	db, err := sqlstore.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if migrations != nil {
		if err := db.Migrate(t.Context(), migrations); err != nil {
			t.Fatalf("migrate test database: %v", err)
		}
	}
	return db
}
