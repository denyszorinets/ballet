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
	"unicode/utf8"

	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Search finds entries about project in organization's knowledge.
func (r *Reader) Search(ctx context.Context, organization, project, query string, limit int) ([]onboarding.Knowledge, error) {
	tok, err := r.token(organization, "", runtoken.CapKnowledgeRead)
	if err != nil {
		return nil, err
	}
	var found struct {
		Items []struct {
			Entry entryJSON `json:"entry"`
		} `json:"items"`
	}
	q := url.Values{"q": {query}, "project": {project}, "limit": {fmt.Sprint(limit)}}
	if err := r.get(ctx, tok, organization, "search", q, &found); err != nil {
		return nil, err
	}
	out := make([]onboarding.Knowledge, 0, len(found.Items))
	for _, h := range found.Items {
		out = append(out, onboarding.Knowledge{ID: h.Entry.ID, Kind: h.Entry.Kind, Title: h.Entry.Title, Body: h.Entry.Body})
	}
	return out, nil
}

// RecordAnswer writes an answered question as a decision entry linked to
// the ticket, authored by author (a human subject, or "planner").
func (r *Reader) RecordAnswer(ctx context.Context, organization, project, ticketKey, author string, q report.Question) error {
	tok, err := r.token(organization, author, runtoken.CapKnowledgeWrite)
	if err != nil {
		return err
	}
	var body strings.Builder
	fmt.Fprintf(&body, "**Question** (asked while working on %s):\n\n%s\n\n", ticketKey, strings.TrimSpace(q.Text))
	if c := strings.TrimSpace(q.Context); c != "" {
		fmt.Fprintf(&body, "**Context:**\n\n%s\n\n", c)
	}
	fmt.Fprintf(&body, "**Answer** (by %s):\n\n%s\n", author, strings.TrimSpace(q.Answer))
	entry := map[string]any{"kind": "decision", "title": "Q: " + oneLine(q.Text, 150), "body": body.String(),
		"projects": []string{project}, "items": []string{ticketKey}}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	u := r.URL.JoinPath("v1", "organizations", organization, "knowledge", "entries")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return fmt.Errorf("knowledge service unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("knowledge entries: %s: %s", resp.Status, msg)
	}
	return nil
}

func (r *Reader) token(organization, actingFor, capability string) (string, error) {
	return r.Tokens.Issue(runtoken.Claims{Kind: runtoken.KindService, Subject: "service:core", Audience: []string{"knowledge"},
		Organization: organization, ActingFor: actingFor, Capabilities: []string{capability}}, time.Minute)
}

func (r *Reader) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return http.DefaultClient
}

// oneLine shortens s to one line of at most n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
