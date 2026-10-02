package github_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/infra/forge/github"
)

// api is a fake GitHub REST API for acme/web.
type api struct {
	mu       sync.Mutex
	pulls    []map[string]any
	checks   string // check-runs JSON
	status   string // combined status JSON
	reviews  string
	calls    []string
	bodies   map[string]map[string]any
	authSeen string
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authSeen = r.Header.Get("Authorization")
	key := r.Method + " " + r.URL.Path
	a.calls = append(a.calls, key)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	a.bodies[key] = body
	p := strings.TrimPrefix(r.URL.Path, "/repos/acme/web")
	switch {
	case r.Method == http.MethodGet && p == "/pulls":
		if r.URL.Query().Get("head") != "acme:ballet/WEB-1-login" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(a.pulls)
	case r.Method == http.MethodPost && p == "/pulls":
		pr := map[string]any{"number": 7, "html_url": "https://github.com/acme/web/pull/7", "title": body["title"],
			"state": "open", "head": map[string]any{"ref": body["head"], "sha": "abc"}, "base": map[string]any{"ref": body["base"]}}
		a.pulls = append(a.pulls, pr)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(pr)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/pulls/") && strings.HasSuffix(p, "/reviews"):
		_, _ = w.Write([]byte(a.reviews))
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/pulls/"):
		for _, pr := range a.pulls {
			if "/pulls/"+jsonNumber(pr["number"]) == p {
				_ = json.NewEncoder(w).Encode(pr)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case strings.HasSuffix(p, "/check-runs"):
		_, _ = w.Write([]byte(a.checks))
	case strings.HasSuffix(p, "/status"):
		_, _ = w.Write([]byte(a.status))
	case strings.HasSuffix(p, "/merge"):
		_, _ = w.Write([]byte(`{"merged":true}`))
	default:
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}
}

func jsonNumber(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func setup(t *testing.T) (*api, github.Adapter, forge.Repo) {
	t.Helper()
	a := &api{bodies: map[string]map[string]any{},
		checks: `{"total_count":0,"check_runs":[]}`, status: `{"state":"pending","total_count":0}`, reviews: `[]`}
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	return a, github.Adapter{APIURL: srv.URL}, forge.Repo{Owner: "acme", Name: "web", Token: "ghp_x"}
}

func TestGitHub_EnsureOpensOrReusesAPullRequest(t *testing.T) {
	a, gh, repo := setup(t)
	pr, err := gh.Ensure(t.Context(), repo, "ballet/WEB-1-login", "main", "WEB-1 Login", "Body")
	require.NoError(t, err)
	assert.Equal(t, 7, pr.Number)
	assert.Equal(t, forge.StateOpen, pr.State)
	assert.Equal(t, forge.ChecksNone, pr.Checks)
	assert.Equal(t, "Bearer ghp_x", a.authSeen)
	assert.Equal(t, map[string]any{"title": "WEB-1 Login", "head": "ballet/WEB-1-login", "base": "main", "body": "Body"},
		a.bodies["POST /repos/acme/web/pulls"])

	again, err := gh.Ensure(t.Context(), repo, "ballet/WEB-1-login", "main", "x", "y")
	require.NoError(t, err)
	assert.Equal(t, 7, again.Number, "the open PR is reused")
	posts := 0
	for _, c := range a.calls {
		if c == "POST /repos/acme/web/pulls" {
			posts++
		}
	}
	assert.Equal(t, 1, posts)
}

func TestGitHub_ChecksAndReviews(t *testing.T) {
	a, gh, repo := setup(t)
	a.pulls = []map[string]any{{"number": 3, "html_url": "u", "state": "closed", "merged_at": "2026-10-01T00:00:00Z",
		"head": map[string]any{"ref": "b", "sha": "s"}, "base": map[string]any{"ref": "main"}}}
	cases := []struct {
		checks, status, reviews string
		wantChecks              forge.Checks
		wantReview              forge.Review
	}{
		{`{"total_count":2,"check_runs":[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"skipped"}]}`,
			`{"state":"pending","total_count":0}`, `[{"user":{"login":"a"},"state":"APPROVED"}]`, forge.ChecksSuccess, forge.ReviewApproved},
		{`{"total_count":1,"check_runs":[{"status":"in_progress","conclusion":null}]}`, `{"state":"success","total_count":1}`,
			`[{"user":{"login":"a"},"state":"APPROVED"},{"user":{"login":"b"},"state":"CHANGES_REQUESTED"}]`, forge.ChecksPending, forge.ReviewChangesRequested},
		{`{"total_count":1,"check_runs":[{"status":"completed","conclusion":"failure"}]}`, `{"state":"success","total_count":0}`,
			`[{"user":{"login":"a"},"state":"CHANGES_REQUESTED"},{"user":{"login":"a"},"state":"APPROVED"}]`, forge.ChecksFailure, forge.ReviewApproved},
		{`{"total_count":0,"check_runs":[]}`, `{"state":"failure","total_count":2}`, `[{"user":{"login":"a"},"state":"COMMENTED"}]`,
			forge.ChecksFailure, forge.ReviewCommented},
	}
	for _, c := range cases {
		a.checks, a.status, a.reviews = c.checks, c.status, c.reviews
		pr, err := gh.Get(t.Context(), repo, forge.PullRequest{Number: 3})
		require.NoError(t, err)
		assert.Equal(t, forge.StateMerged, pr.State)
		assert.Equal(t, c.wantChecks, pr.Checks, c.checks)
		assert.Equal(t, c.wantReview, pr.Review, c.reviews)
	}
}

func TestGitHub_CommentReviewMerge(t *testing.T) {
	a, gh, repo := setup(t)
	pr := forge.PullRequest{Number: 7}
	require.NoError(t, gh.Comment(t.Context(), repo, pr, "Hi"))
	require.NoError(t, gh.Review(t.Context(), repo, pr, forge.ReviewApprove, "LGTM"))
	require.NoError(t, gh.Merge(t.Context(), repo, pr, "WEB-1 Login (#7)"))
	assert.Equal(t, map[string]any{"body": "Hi"}, a.bodies["POST /repos/acme/web/issues/7/comments"])
	assert.Equal(t, map[string]any{"event": "APPROVE", "body": "LGTM"}, a.bodies["POST /repos/acme/web/pulls/7/reviews"])
	assert.Equal(t, map[string]any{"merge_method": "squash", "commit_title": "WEB-1 Login (#7)"}, a.bodies["PUT /repos/acme/web/pulls/7/merge"])

	_, err := gh.Get(t.Context(), repo, forge.PullRequest{Number: 99})
	assert.ErrorContains(t, err, "404")
}

// TestGitHub_LiveReadOnly reads a merged pull request of a real repository
// (BALLET_GITHUB_LIVE=owner/name#number, token from `gh auth token`).
func TestGitHub_LiveReadOnly(t *testing.T) {
	spec := os.Getenv("BALLET_GITHUB_LIVE")
	if spec == "" {
		t.Skip("set BALLET_GITHUB_LIVE=owner/name#number to read a real pull request")
	}
	tok, err := exec.Command("gh", "auth", "token").Output()
	require.NoError(t, err)
	repoPart, num, _ := strings.Cut(spec, "#")
	owner, name, _ := strings.Cut(repoPart, "/")
	var n int
	_, err = fmtSscan(num, &n)
	require.NoError(t, err)
	pr, err := github.Adapter{}.Get(t.Context(), forge.Repo{Owner: owner, Name: name, Token: strings.TrimSpace(string(tok))},
		forge.PullRequest{Number: n})
	require.NoError(t, err)
	t.Logf("%+v", pr)
	assert.Equal(t, forge.StateMerged, pr.State)
	assert.Equal(t, forge.ChecksSuccess, pr.Checks)
}

func fmtSscan(s string, n *int) (int, error) { return fmt.Sscan(s, n) }
