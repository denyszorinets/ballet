// Package skill defines agent skills (ADR-0010): named, versioned bundles
// of instructions (a SKILL.md body plus supporting text files) at
// organization, customer or project scope. Published versions are
// immutable; the draft is what admins edit.
package skill

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ScopeKind is the level a skill belongs to.
type ScopeKind string

// Scope kinds.
const (
	ScopeOrganization ScopeKind = "organization"
	ScopeCustomer     ScopeKind = "customer"
	ScopeProject      ScopeKind = "project"
)

// Scope of a skill; Customer is set for customer and project scopes.
type Scope struct {
	Kind     ScopeKind
	Customer string // customer key
	Project  string // project key
}

// String formats the scope like role binding scopes.
func (s Scope) String() string {
	switch s.Kind {
	case ScopeCustomer:
		return "customer:" + s.Customer
	case ScopeProject:
		return "project:" + s.Project
	}
	return string(ScopeOrganization)
}

// ParseScope parses "organization", "customer:<key>" or "project:<key>";
// the customer of a project scope is resolved by the caller.
func ParseScope(s string) (Scope, error) {
	if s == string(ScopeOrganization) {
		return Scope{Kind: ScopeOrganization}, nil
	}
	kind, key, ok := strings.Cut(s, ":")
	if ok && key != "" {
		switch ScopeKind(kind) {
		case ScopeCustomer:
			return Scope{Kind: ScopeCustomer, Customer: key}, nil
		case ScopeProject:
			return Scope{Kind: ScopeProject, Project: key}, nil
		}
	}
	return Scope{}, fmt.Errorf("scope %q must be organization, customer:<key> or project:<key>", s)
}

// Content is what a version (or the draft) contains.
type Content struct {
	Description string            // when to use the skill; agents decide relevance by it
	Body        string            // SKILL.md body, Markdown
	Files       map[string]string // supporting text files by relative path
}

// Skill is a named skill with its draft.
type Skill struct {
	ID            string
	Scope         Scope
	Name          string
	Draft         Content
	LatestVersion int64 // 0: never published
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Version       int64 // optimistic concurrency of the draft
}

// Version is an immutable published version.
type Version struct {
	Number      int64
	Content     Content
	PublishedBy string
	PublishedAt time.Time
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// Limits.
const (
	maxDescription = 1024
	maxBody        = 100_000
	maxFiles       = 50
	maxFilesBytes  = 1 << 20
)

// ValidateName checks a skill name: 2-64 lowercase letters, digits and
// single hyphens, starting with a letter.
func ValidateName(n string) error {
	if len(n) < 2 || len(n) > 64 || !nameRe.MatchString(n) {
		return fmt.Errorf("name %q must be 2-64 lowercase letters, digits or single hyphens, starting with a letter", n)
	}
	return nil
}

// Validate checks content limits and file paths.
func (c Content) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Description) == "" || utf8.RuneCountInString(c.Description) > maxDescription {
		errs = append(errs, fmt.Errorf("description must be 1-%d characters", maxDescription))
	}
	if utf8.RuneCountInString(c.Body) > maxBody {
		errs = append(errs, fmt.Errorf("body must be at most %d characters", maxBody))
	}
	if len(c.Files) > maxFiles {
		errs = append(errs, fmt.Errorf("at most %d files", maxFiles))
	}
	total := 0
	for p, content := range c.Files {
		total += len(content)
		if err := validatePath(p); err != nil {
			errs = append(errs, err)
		}
		if !utf8.ValidString(content) {
			errs = append(errs, fmt.Errorf("file %q must be UTF-8 text", p))
		}
	}
	if total > maxFilesBytes {
		errs = append(errs, fmt.Errorf("files must total at most %d bytes", maxFilesBytes))
	}
	return errors.Join(errs...)
}

// validatePath accepts clean relative paths inside the skill directory,
// excluding SKILL.md itself (that is the body).
func validatePath(p string) error {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == ".." ||
		strings.Contains(p, "\\") || strings.EqualFold(p, "SKILL.md") {
		return fmt.Errorf("invalid file path %q: must be a clean relative path other than SKILL.md", p)
	}
	return nil
}
