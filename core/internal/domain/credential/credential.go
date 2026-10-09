// Package credential defines LLM provider credentials (ADR-0011): a
// organization default per provider, optionally overridden per project.
package credential

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Provider names an LLM API.
type Provider string

// Providers.
const (
	ProviderAnthropic Provider = "anthropic" // messages
	ProviderOpenAI    Provider = "openai"    // OpenAI-compatible API (embeddings)
	ProviderGit       Provider = "git"       // token for cloning and pushing the project repository
)

var providers = []Provider{ProviderAnthropic, ProviderOpenAI, ProviderGit}

// LLM reports whether p is an LLM provider (served to the gateway).
func (p Provider) LLM() bool { return p == ProviderAnthropic || p == ProviderOpenAI }

// Credential is a provider API key at organization or project scope. APIKey is
// only populated when resolved for the gateway.
type Credential struct {
	ID             string
	OrganizationID string
	ProjectID      string // empty: organization default
	Provider       Provider
	APIKey         string
	BaseURL        string // empty: provider default
	Fingerprint    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Fingerprint identifies a key without revealing it: a short hash plus the
// last four characters.
func Fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	tail := key
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return hex.EncodeToString(sum[:4]) + "…" + tail
}

// Validate checks provider, key and base URL.
func Validate(p Provider, key, baseURL string) error {
	var errs []error
	if !slices.Contains(providers, p) {
		errs = append(errs, fmt.Errorf("provider %q must be anthropic, openai or git", p))
	}
	if strings.TrimSpace(key) == "" || len(key) > 1000 {
		errs = append(errs, errors.New("api_key must be 1-1000 characters"))
	}
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, errors.New("base_url must be an http(s) URL"))
		}
	}
	return errors.Join(errs...)
}
