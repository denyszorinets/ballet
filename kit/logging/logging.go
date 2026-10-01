// Package logging creates the structured JSON loggers used by Ballet services.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// ParseLevel converts debug, info, warn or error into a slog level.
func ParseLevel(level string) (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		return 0, fmt.Errorf("unknown log level %q (want debug, info, warn or error)", level)
	}
	return l, nil
}

// New returns a JSON logger writing to w at the given level, tagging every
// record with the service name.
func New(w io.Writer, service, level string) (*slog.Logger, error) {
	l, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l})
	return slog.New(h).With("service", service), nil
}
