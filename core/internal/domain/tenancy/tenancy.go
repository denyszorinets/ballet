// Package tenancy defines organizations and projects (ADR-0004). A Ballet
// installation is one platform; the organization is the isolation
// boundary; projects belong to exactly one organization.
package tenancy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Organization is a client of the platform and the isolation boundary.
type Organization struct {
	ID   string
	Key  string // immutable, e.g. "acme"; used in URLs and role binding scopes
	Name string
	// FeaturePolicy says how agents may change the organization's
	// features: "direct", "proposal" or "read_only" (ADR-0028).
	FeaturePolicy string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Version       int64
}

// Project is a product built for an organization.
type Project struct {
	ID             string
	OrganizationID string
	Key            string // immutable, e.g. "ACME"; prefix of ticket keys (ACME-42)
	Name           string
	Description    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Version        int64
}

var (
	organizationKeyRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	projectKeyRe      = regexp.MustCompile(`^[A-Z][A-Z0-9]+$`)
)

// ValidateOrganizationKey checks an organization key: 2–32 characters, lowercase
// letters, digits and single hyphens, starting with a letter.
func ValidateOrganizationKey(key string) error {
	if len(key) < 2 || len(key) > 32 || !organizationKeyRe.MatchString(key) {
		return fmt.Errorf("organization key %q must be 2-32 lowercase letters, digits or single hyphens, starting with a letter", key)
	}
	return nil
}

// ValidateProjectKey checks a project key: 2–10 uppercase letters and
// digits, starting with a letter.
func ValidateProjectKey(key string) error {
	if len(key) < 2 || len(key) > 10 || !projectKeyRe.MatchString(key) {
		return fmt.Errorf("project key %q must be 2-10 uppercase letters or digits, starting with a letter", key)
	}
	return nil
}

// ValidateName checks a display name: non-blank, at most 200 characters.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("name must not be empty")
	}
	if utf8.RuneCountInString(name) > 200 {
		return errors.New("name must be at most 200 characters")
	}
	return nil
}

// ValidateDescription checks a description: at most 10000 characters.
func ValidateDescription(d string) error {
	if utf8.RuneCountInString(d) > 10000 {
		return errors.New("description must be at most 10000 characters")
	}
	return nil
}
