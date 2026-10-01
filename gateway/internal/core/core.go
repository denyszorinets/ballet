// Package core is the gateway's client of Core's internal API.
package core

import (
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

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	cred    Credential
	expires time.Time
}

// ResolveCredential returns the credential for a customer/project and
// provider, cached for CacheTTL.
func (c *Client) ResolveCredential(ctx context.Context, customer, project, provider string) (Credential, error) {
	key := customer + "/" + project + "/" + provider
	c.mu.Lock()
	if e, ok := c.cache[key]; ok && time.Now().Before(e.expires) {
		c.mu.Unlock()
		return e.cred, nil
	}
	c.mu.Unlock()

	q := url.Values{"customer": {customer}, "project": {project}, "provider": {provider}}
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
	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("core %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("core %s: %s", path, resp.Status)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}
