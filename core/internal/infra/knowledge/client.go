// Package knowledge calls the Knowledge service's REST API on behalf of a
// human (or the planner acting for one), with a request-scoped token
// carrying only the capabilities the human holds (ADR-0022).
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Client calls Knowledge for the caller in ctx.
type Client struct {
	URL    *url.URL // Knowledge base URL
	Access *app.KnowledgeAccess
	Tokens *runtoken.TokenIssuer
	HTTP   *http.Client
}

const tokenTTL = 5 * time.Minute

// Do authorizes the caller for the customer's knowledge (knowledge.write
// for writes), then sends method to /v1/customers/{customer}/knowledge/{path}
// and returns the response body. Error responses map to app errors.
func (c *Client) Do(ctx context.Context, customer string, write bool, method, path string, query url.Values, body any) (json.RawMessage, error) {
	g, err := c.Access.Authorize(ctx, customer, write)
	if err != nil {
		return nil, err
	}
	claims := runtoken.Claims{
		Kind: runtoken.KindService, Subject: "service:core", Audience: []string{"knowledge"},
		Customer: g.Customer, ActingFor: g.ActingFor, Capabilities: g.Capabilities,
	}
	if session, ok := app.PlannerSessionOf(ctx); ok {
		claims.Subject = "planner:" + session // Core forwarding for the planner
	}
	tok, err := c.Tokens.Issue(claims, tokenTTL)
	if err != nil {
		return nil, err
	}
	u := c.URL.JoinPath("v1", "customers", customer, "knowledge", path)
	u.RawQuery = query.Encode()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("knowledge: encode request: %w", err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("knowledge service unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("knowledge: read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		msg := strings.TrimSpace(e.Message)
		switch resp.StatusCode {
		case http.StatusBadRequest:
			return nil, fmt.Errorf("%w: %s", app.ErrInvalid, msg)
		case http.StatusNotFound:
			return nil, fmt.Errorf("%w: %s", app.ErrNotFound, msg)
		case http.StatusConflict:
			return nil, fmt.Errorf("%w: %s", app.ErrConflict, msg)
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, fmt.Errorf("%w: %s", app.ErrForbidden, msg)
		}
		return nil, fmt.Errorf("knowledge: %s: %s", resp.Status, msg)
	}
	return data, nil
}
