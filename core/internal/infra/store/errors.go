package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// mapWriteErr translates database errors of a write batch into app errors.
func mapWriteErr(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sqlstore.ErrConflict):
		return fmt.Errorf("%s: %w", what, app.ErrConflict)
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		return fmt.Errorf("%s: %w", what, app.ErrAlreadyExists)
	default:
		return fmt.Errorf("%s: %w", what, err)
	}
}

// mapReadErr translates a single-row read error into app errors.
func mapReadErr(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%s: %w", what, app.ErrNotFound)
	default:
		return fmt.Errorf("%s: %w", what, err)
	}
}
