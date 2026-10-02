// Package github is the GitHub forge adapter (REST API v3): pull requests,
// their checks and reviews, comments, reviews and squash merges.
package github

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

	"github.com/denyszorinets/ballet/core/internal/domain/forge"
)

// Name of the adapter.
const Name = "github"

// Adapter talks to GitHub or GitHub Enterprise.
type Adapter struct {
	APIURL string // default https://api.github.com
	HTTP   *http.Client
}

// Name returns "github".
func (Adapter) Name() string { return Name }

func (a Adapter) do(ctx context.Context, r forge.Repo, method, path string, in, out any) error {
	base := strings.TrimSuffix(a.APIURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := a.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("github: %s %s: %d %s", method, path, resp.StatusCode, e.Message)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type pullJSON struct {
	Number    int       `json:"number"`
	HTMLURL   string    `json:"html_url"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	Merged    bool      `json:"merged"`
	MergedAt  *string   `json:"merged_at"`
	Draft     bool      `json:"draft"`
	Mergeable *bool     `json:"mergeable"`
	UpdatedAt time.Time `json:"updated_at"`
	Head      struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (p pullJSON) pr() forge.PullRequest {
	state := forge.State(p.State)
	if p.Merged || p.MergedAt != nil {
		state = forge.StateMerged
	}
	return forge.PullRequest{Number: p.Number, URL: p.HTMLURL, Title: p.Title, Head: p.Head.Ref, Base: p.Base.Ref,
		HeadSHA: p.Head.SHA, State: state, Draft: p.Draft, Mergeable: p.Mergeable, UpdatedAt: p.UpdatedAt,
		Checks: forge.ChecksNone, Review: forge.ReviewNone}
}

func repoPath(r forge.Repo) string {
	return "/repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name)
}

// Ensure returns the open (or merged) pull request of head into base,
// opening one when there is none.
func (a Adapter) Ensure(ctx context.Context, r forge.Repo, head, base, title, body string) (forge.PullRequest, error) {
	var existing []pullJSON
	q := url.Values{"head": {r.Owner + ":" + head}, "base": {base}, "state": {"all"}, "sort": {"created"}, "direction": {"desc"}}
	if err := a.do(ctx, r, http.MethodGet, repoPath(r)+"/pulls?"+q.Encode(), nil, &existing); err != nil {
		return forge.PullRequest{}, err
	}
	for _, p := range existing {
		if p.State == "open" || p.MergedAt != nil {
			return a.Get(ctx, r, p.pr())
		}
	}
	// GitHub refuses a pull request of a missing head branch (422).
	var branch struct {
		Name string `json:"name"`
	}
	if err := a.do(ctx, r, http.MethodGet, repoPath(r)+"/branches/"+url.PathEscape(head), nil, &branch); err != nil {
		if strings.Contains(err.Error(), ": 404 ") {
			return forge.PullRequest{}, fmt.Errorf("%w: %s", forge.ErrNoBranch, head)
		}
		return forge.PullRequest{}, err
	}
	var created pullJSON
	if err := a.do(ctx, r, http.MethodPost, repoPath(r)+"/pulls",
		map[string]any{"title": title, "head": head, "base": base, "body": body}, &created); err != nil {
		return forge.PullRequest{}, err
	}
	return a.Get(ctx, r, created.pr())
}

// Get refreshes a pull request with its checks and reviews.
func (a Adapter) Get(ctx context.Context, r forge.Repo, pr forge.PullRequest) (forge.PullRequest, error) {
	var p pullJSON
	if err := a.do(ctx, r, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repoPath(r), pr.Number), nil, &p); err != nil {
		return forge.PullRequest{}, err
	}
	out := p.pr()
	var err error
	if out.Checks, err = a.checks(ctx, r, p.Head.SHA); err != nil {
		return forge.PullRequest{}, err
	}
	if out.Review, err = a.review(ctx, r, p.Number); err != nil {
		return forge.PullRequest{}, err
	}
	return out, nil
}

// checks combines check runs and commit statuses of sha.
func (a Adapter) checks(ctx context.Context, r forge.Repo, sha string) (forge.Checks, error) {
	var runs struct {
		Total int `json:"total_count"`
		Runs  []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := a.do(ctx, r, http.MethodGet, repoPath(r)+"/commits/"+sha+"/check-runs?per_page=100", nil, &runs); err != nil {
		return "", err
	}
	var status struct {
		State string `json:"state"`
		Total int    `json:"total_count"`
	}
	if err := a.do(ctx, r, http.MethodGet, repoPath(r)+"/commits/"+sha+"/status", nil, &status); err != nil {
		return "", err
	}
	if runs.Total == 0 && status.Total == 0 {
		return forge.ChecksNone, nil
	}
	result := forge.ChecksSuccess
	for _, c := range runs.Runs {
		switch {
		case c.Status != "completed":
			result = forge.ChecksPending
		case c.Conclusion == "failure" || c.Conclusion == "cancelled" || c.Conclusion == "timed_out" ||
			c.Conclusion == "action_required" || c.Conclusion == "startup_failure":
			return forge.ChecksFailure, nil
		}
	}
	if status.Total > 0 {
		switch status.State {
		case "failure", "error":
			return forge.ChecksFailure, nil
		case "pending":
			result = forge.ChecksPending
		}
	}
	return result, nil
}

// review summarises each reviewer's latest verdict.
func (a Adapter) review(ctx context.Context, r forge.Repo, number int) (forge.Review, error) {
	var reviews []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		State string `json:"state"`
	}
	if err := a.do(ctx, r, http.MethodGet, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", repoPath(r), number), nil, &reviews); err != nil {
		return "", err
	}
	latest := map[string]string{}
	commented := false
	for _, rv := range reviews {
		switch rv.State {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
			latest[rv.User.Login] = rv.State
		case "COMMENTED":
			commented = true
		}
	}
	result := forge.ReviewNone
	for _, s := range latest {
		if s == "CHANGES_REQUESTED" {
			return forge.ReviewChangesRequested, nil
		}
		if s == "APPROVED" {
			result = forge.ReviewApproved
		}
	}
	if result == forge.ReviewNone && commented {
		result = forge.ReviewCommented
	}
	return result, nil
}

// Comment posts a comment on the pull request.
func (a Adapter) Comment(ctx context.Context, r forge.Repo, pr forge.PullRequest, body string) error {
	return a.do(ctx, r, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", repoPath(r), pr.Number), map[string]string{"body": body}, nil)
}

// Review posts a review.
func (a Adapter) Review(ctx context.Context, r forge.Repo, pr forge.PullRequest, event forge.ReviewEvent, body string) error {
	return a.do(ctx, r, http.MethodPost, fmt.Sprintf("%s/pulls/%d/reviews", repoPath(r), pr.Number),
		map[string]string{"event": string(event), "body": body}, nil)
}

// Merge squash-merges the pull request.
func (a Adapter) Merge(ctx context.Context, r forge.Repo, pr forge.PullRequest, title string) error {
	return a.do(ctx, r, http.MethodPut, fmt.Sprintf("%s/pulls/%d/merge", repoPath(r), pr.Number),
		map[string]string{"merge_method": "squash", "commit_title": title}, nil)
}
