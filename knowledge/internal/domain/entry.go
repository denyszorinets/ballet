// Package domain defines knowledge entries: documents, decisions, notes
// and technical debt records of one customer's knowledge space.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind classifies an entry.
type Kind string

// Entry kinds.
const (
	KindDocument Kind = "document"
	KindDecision Kind = "decision"
	KindNote     Kind = "note"
	KindDebt     Kind = "debt"
)

var kinds = []Kind{KindDocument, KindDecision, KindNote, KindDebt}

// Entry is a knowledge entry. Customer is the knowledge space.
type Entry struct {
	ID        string
	Customer  string // customer key
	Kind      Kind
	Title     string
	Body      string   // Markdown
	Projects  []string // project keys the entry concerns
	Items     []string // linked tracker items, e.g. WEB-42
	Version   int64
	CreatedBy string
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Version is an immutable snapshot of an entry.
type Version struct {
	Version   int64
	Title     string
	Body      string
	Author    string
	CreatedAt time.Time
}

var (
	projectKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
	itemKeyRe    = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}-[1-9][0-9]*$`)
)

// Validate checks an entry's content.
func (e Entry) Validate() error {
	var errs []error
	if !slices.Contains(kinds, e.Kind) {
		errs = append(errs, fmt.Errorf("kind %q must be document, decision, note or debt", e.Kind))
	}
	if strings.TrimSpace(e.Title) == "" || utf8.RuneCountInString(e.Title) > 300 {
		errs = append(errs, errors.New("title must be 1-300 characters"))
	}
	if utf8.RuneCountInString(e.Body) > 200_000 {
		errs = append(errs, errors.New("body must be at most 200000 characters"))
	}
	for _, p := range e.Projects {
		if !projectKeyRe.MatchString(p) {
			errs = append(errs, fmt.Errorf("invalid project key %q", p))
		}
	}
	for _, it := range e.Items {
		if !itemKeyRe.MatchString(it) {
			errs = append(errs, fmt.Errorf("invalid item key %q", it))
		}
	}
	return errors.Join(errs...)
}
