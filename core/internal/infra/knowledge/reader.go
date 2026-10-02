package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Reader reads knowledge for Core itself (onboarding bundles), with a
// short-lived Core service token limited to reading one customer's space.
type Reader struct {
	URL    *url.URL
	Tokens *runtoken.TokenIssuer
	HTTP   *http.Client
}

type entryJSON struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// ForTicket returns the entries linked to ticket and up to limit more
// found by searching query in the project.
func (r *Reader) ForTicket(ctx context.Context, customer, project, ticket, query string, limit int) ([]onboarding.Knowledge, error) {
	tok, err := r.Tokens.Issue(runtoken.Claims{Kind: runtoken.KindService, Subject: "service:core",
		Audience: []string{"knowledge"}, Customer: customer, Capabilities: []string{runtoken.CapKnowledgeRead}}, time.Minute)
	if err != nil {
		return nil, err
	}
	var linked struct {
		Items []entryJSON `json:"items"`
	}
	if err := r.get(ctx, tok, customer, "entries", url.Values{"item": {ticket}}, &linked); err != nil {
		return nil, err
	}
	out := make([]onboarding.Knowledge, 0, len(linked.Items)+limit)
	seen := map[string]bool{}
	for _, e := range linked.Items {
		seen[e.ID] = true
		out = append(out, onboarding.Knowledge{ID: e.ID, Kind: e.Kind, Title: e.Title, Body: e.Body, Linked: true})
	}
	if query == "" || limit <= 0 {
		return out, nil
	}
	var found struct {
		Items []struct {
			Entry entryJSON `json:"entry"`
		} `json:"items"`
	}
	q := url.Values{"q": {query}, "project": {project}, "limit": {fmt.Sprint(limit + len(linked.Items))}}
	if err := r.get(ctx, tok, customer, "search", q, &found); err != nil {
		return nil, err
	}
	for _, h := range found.Items {
		if seen[h.Entry.ID] || limit == 0 {
			continue
		}
		limit--
		out = append(out, onboarding.Knowledge{ID: h.Entry.ID, Kind: h.Entry.Kind, Title: h.Entry.Title, Body: h.Entry.Body})
	}
	return out, nil
}

func (r *Reader) get(ctx context.Context, tok, customer, path string, q url.Values, out any) error {
	u := r.URL.JoinPath("v1", "customers", customer, "knowledge", path)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	hc := r.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("knowledge service unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("knowledge %s: %s: %s", path, resp.Status, data)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
