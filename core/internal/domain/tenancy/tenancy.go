// Package tenancy defines customers and projects (ADR-0004). A Ballet
// installation is one organization; the customer is the isolation
// boundary; projects belong to exactly one customer.
package tenancy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Customer is a client of the organization and the isolation boundary.
type Customer struct {
	ID        string
	Key       string // immutable, e.g. "acme"; used in URLs and role binding scopes
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// Project is a product built for a customer.
type Project struct {
	ID          string
	CustomerID  string
	Key         string // immutable, e.g. "ACME"; prefix of ticket keys (ACME-42)
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int64
}

var (
	customerKeyRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	projectKeyRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]+$`)
)

// ValidateCustomerKey checks a customer key: 2–32 characters, lowercase
// letters, digits and single hyphens, starting with a letter.
func ValidateCustomerKey(key string) error {
	if len(key) < 2 || len(key) > 32 || !customerKeyRe.MatchString(key) {
		return fmt.Errorf("customer key %q must be 2-32 lowercase letters, digits or single hyphens, starting with a letter", key)
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
