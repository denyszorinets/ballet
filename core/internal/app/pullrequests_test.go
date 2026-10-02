package app_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/auth"
)

// memForge is an in-memory forge.
type memForge struct {
	mu       sync.Mutex
	prs      map[string]forge.PullRequest // by head
	opened   int
	merged   []int
	repos    []forge.Repo
	failGet  bool
	checks   forge.Checks // of new pull requests; "": pending
	noBranch bool         // the ticket branch was never pushed
}

func (m *memForge) Name() string { return "github" }

func (m *memForge) Ensure(_ context.Context, r forge.Repo, head, base, title, _ string) (forge.PullRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repos = append(m.repos, r)
	if pr, ok := m.prs[head]; ok {
		return pr, nil
	}
	if m.noBranch {
		return forge.PullRequest{}, fmt.Errorf("%w: %s", forge.ErrNoBranch, head)
	}
	m.opened++
	checks := m.checks
	if checks == "" {
		checks = forge.ChecksPending
	}
	pr := forge.PullRequest{Number: m.opened, URL: "https://forge/pr", Title: title, Head: head, Base: base, HeadSHA: "a",
		State: forge.StateOpen, Checks: checks, Review: forge.ReviewNone}
	m.prs[head] = pr
	return pr, nil
}

func (m *memForge) Get(_ context.Context, _ forge.Repo, pr forge.PullRequest) (forge.PullRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGet {
		return forge.PullRequest{}, errors.New("forge down")
	}
	return m.prs[pr.Head], nil
}

func (m *memForge) Comment(context.Context, forge.Repo, forge.PullRequest, string) error { return nil }
func (m *memForge) Review(context.Context, forge.Repo, forge.PullRequest, forge.ReviewEvent, string) error {
	return nil
}

func (m *memForge) Merge(_ context.Context, _ forge.Repo, pr forge.PullRequest, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.merged = append(m.merged, pr.Number)
	p := m.prs[pr.Head]
	p.State = forge.StateMerged
	m.prs[pr.Head] = p
	return nil
}

func (m *memForge) set(head string, f func(*forge.PullRequest)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.prs[head]
	f(&p)
	m.prs[head] = p
}

func TestPullRequests_OpenRefreshMergePoll(t *testing.T) {
	tr, env := newTracker(t)
	mf := &memForge{prs: map[string]forge.PullRequest{}}
	ps := &app.PullRequests{Store: env.store, Execution: env.store, Items: env.store, Tenancy: env.store, Authz: env.rbac,
		Forges: func(execution.Settings) (forge.Forge, error) { return mf, nil },
		Token:  func(context.Context, string, string) (string, error) { return "ghp", nil }, Now: time.Now}
	dave := user(t, "dave", "acme-admins")
	bob := user(t, "bob", "acme-devs")
	carol := user(t, "carol", "acme-viewers")
	tk, err := tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Login"})
	require.NoError(t, err)

	_, err = ps.Open(bob, tk.Key)
	assert.ErrorIs(t, err, app.ErrInvalid, "no repository yet")
	ex := &app.Execution{Store: env.store, Tenancy: env.store, Authz: env.rbac, Now: time.Now}
	_, err = ex.Set(dave, "WEB", execution.Settings{RepoURL: "https://github.com/acme/web.git", DefaultBranch: "main"}, 0)
	require.NoError(t, err)

	_, err = ps.Open(carol, tk.Key)
	assert.ErrorIs(t, err, app.ErrForbidden)
	pr, err := ps.Open(bob, tk.Key)
	require.NoError(t, err)
	assert.Equal(t, 1, pr.Number)
	assert.Equal(t, "ballet/"+tk.Key+"-login", pr.Head)
	assert.Equal(t, "main", pr.Base)
	assert.Equal(t, forge.Repo{URL: "https://github.com/acme/web.git", Owner: "acme", Name: "web", Token: "ghp"}, mf.repos[0])
	again, err := ps.Open(bob, tk.Key)
	require.NoError(t, err)
	assert.Equal(t, 1, again.Number)
	assert.Equal(t, 1, mf.opened)

	got, err := ps.Get(carol, tk.Key)
	require.NoError(t, err, "viewers read it")
	assert.Equal(t, forge.ChecksPending, got.Checks)

	// The poller notices CI finishing.
	mf.set(pr.Head, func(p *forge.PullRequest) { p.Checks = forge.ChecksSuccess })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ps.Poll(ctx, 20*time.Millisecond); close(done) }()
	require.Eventually(t, func() bool { g, _ := ps.Get(carol, tk.Key); return g.Checks == forge.ChecksSuccess }, 5*time.Second, 10*time.Millisecond)
	cancel()
	<-done
	history, err := tr.ItemHistory(carol, tk.Key)
	require.NoError(t, err)
	var system bool
	for _, e := range history {
		if e.Type == "item.pr_updated" && e.Actor.Kind == "system" {
			system = true
		}
	}
	assert.True(t, system, "the poller records the change")

	service := auth.WithIdentity(context.Background(), auth.Identity{Kind: auth.KindService, Subject: "svc"})
	sps := *ps
	sps.Authz = fakeAuthz{app.ActTrackerWrite: {"*"}}
	_, err = sps.Merge(service, tk.Key)
	assert.ErrorIs(t, err, app.ErrForbidden, "only humans merge here")
	merged, err := ps.Merge(bob, tk.Key)
	require.NoError(t, err)
	assert.Equal(t, forge.StateMerged, merged.State)
	_, err = ps.Merge(bob, tk.Key)
	assert.ErrorIs(t, err, app.ErrConflict)

	mf.failGet = true
	_, err = ps.Refresh(bob, tk.Key)
	assert.ErrorIs(t, err, app.ErrUnavailable)
}
