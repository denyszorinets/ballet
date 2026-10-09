// Package core is the gateway's client of Core's internal API.
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ErrNoCredential means no credential is configured for the scope.
var ErrNoCredential = errors.New("no credential configured")

// Credential is a resolved provider credential.
type Credential struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
}

// Client calls Core's internal API with the gateway's service token.
type Client struct {
	BaseURL string
	Token   func(context.Context) (string, error)
	HTTP    *http.Client
	// CacheTTL bounds how long resolved credentials are reused (default 1m).
	CacheTTL time.Duration

	// BudgetTTL bounds how long a budget verdict is reused (default 30s).
	BudgetTTL time.Duration

	mu      sync.Mutex
	cache   map[string]cached
	budgets map[string]budgetVerdict
}

type budgetVerdict struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
	expires time.Time
}

// CheckBudget asks Core whether work of an organization/project (and ticket)
// may still call the LLM, cached for BudgetTTL.
func (c *Client) CheckBudget(ctx context.Context, organization, project, ticket string) (bool, string, error) {
	key := organization + "/" + project + "/" + ticket
	c.mu.Lock()
	if v, ok := c.budgets[key]; ok && time.Now().Before(v.expires) {
		c.mu.Unlock()
		return v.Allowed, v.Reason, nil
	}
	c.mu.Unlock()
	q := url.Values{"organization": {organization}, "project": {project}, "ticket": {ticket}}
	var v budgetVerdict
	if _, err := c.get(ctx, "/internal/v1/budget/check?"+q.Encode(), &v); err != nil {
		return false, "", err
	}
	ttl := c.BudgetTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	v.expires = time.Now().Add(ttl)
	c.mu.Lock()
	if c.budgets == nil {
		c.budgets = map[string]budgetVerdict{}
	}
	c.budgets[key] = v
	c.mu.Unlock()
	return v.Allowed, v.Reason, nil
}

type cached struct {
	cred    Credential
	expires time.Time
}

// ResolveCredential returns the credential for an organization/project and
// provider, cached for CacheTTL.
func (c *Client) ResolveCredential(ctx context.Context, organization, project, provider string) (Credential, error) {
	key := organization + "/" + project + "/" + provider
	c.mu.Lock()
	if e, ok := c.cache[key]; ok && time.Now().Before(e.expires) {
		c.mu.Unlock()
		return e.cred, nil
	}
	c.mu.Unlock()

	q := url.Values{"organization": {organization}, "project": {project}, "provider": {provider}}
	var cred Credential
	status, err := c.get(ctx, "/internal/v1/credentials/resolve?"+q.Encode(), &cred)
	if status == http.StatusNotFound {
		return Credential{}, ErrNoCredential
	}
	if err != nil {
		return Credential{}, err
	}
	ttl := c.CacheTTL
	if ttl <= 0 {
		ttl = time.Minute
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]cached{}
	}
	c.cache[key] = cached{cred: cred, expires: time.Now().Add(ttl)}
	c.mu.Unlock()
	return cred, nil
}

// Post sends body as JSON to an internal endpoint and expects 2xx.
func (c *Client) Post(ctx context.Context, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	tok, err := c.Token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("core %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("core %s: %s", path, resp.Status)
	}
	return nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) get(ctx context.Context, path string, out any) (int, error) {
	tok, err := c.Token(ctx)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, fmt.Errorf("core %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("core %s: %s", path, resp.Status)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}
