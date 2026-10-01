// Package app holds Core's use cases. It depends on the domain and on
// interfaces (ports) it defines for persistence and authorization.
package app

import (
	"errors"
	"fmt"
)

// Error kinds returned by use cases. Transports map them to status codes.
var (
	ErrInvalid       = errors.New("invalid argument")
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrConflict      = errors.New("conflicting concurrent update")
	ErrForbidden     = errors.New("forbidden")
	ErrUnauthorized  = errors.New("unauthenticated")
)

// invalid wraps a validation error as ErrInvalid, keeping its message.
func invalid(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalid, err)
}
