package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

func TestExecution_GetAndSet(t *testing.T) {
	_, env := newTracker(t)
	ex := &app.Execution{Store: env.store, Tenancy: env.store, Authz: env.rbac, Now: time.Now}
	dave := user(t, "dave", "acme-admins")
	bob := user(t, "bob", "acme-devs")

	got, err := ex.Get(bob, "WEB")
	require.NoError(t, err)
	assert.Zero(t, got.Version)
	assert.Equal(t, execution.DefaultBranchTemplate, got.BranchTemplate)

	in := execution.Settings{RepoURL: "https://github.com/acme/web.git", DefaultBranch: "main",
		Setup: []string{"make deps"}, Env: map[string]string{"CI": "1"}}
	_, err = ex.Set(bob, "WEB", in, 0)
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers do not configure execution")
	set, err := ex.Set(dave, "WEB", in, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), set.Version)
	_, err = ex.Set(dave, "WEB", in, 0)
	assert.ErrorIs(t, err, app.ErrConflict)
	bad := in
	bad.RepoURL = "https://u:p@github.com/x"
	_, err = ex.Set(dave, "WEB", bad, 1)
	assert.ErrorIs(t, err, app.ErrInvalid)

	got, err = ex.Get(bob, "WEB")
	require.NoError(t, err)
	assert.Equal(t, []string{"make deps"}, got.Setup)
}

func TestRuns_PreparedFromExecutionSettingsWithSecretsOnlyAtStart(t *testing.T) {
	e := newRuns(t, time.Minute)
	ex := &app.Execution{Store: e.st, Tenancy: e.st, Authz: e.runs.Authz, Now: time.Now}
	e.runs.Execution = e.st
	e.d.SecretEnv = func(context.Context, run.Run) (map[string]string, error) {
		return map[string]string{execution.TokenEnv: "s3cret"}, nil
	}
	dave := user(t, "dave", "acme-admins")
	_, err := ex.Set(dave, "WEB", execution.Settings{RepoURL: "https://github.com/acme/web.git", DefaultBranch: "main",
		Env: map[string]string{"CI": "1", "MODE": "project"}}, 0)
	require.NoError(t, err)
	tk, err := e.tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Export invoices"})
	require.NoError(t, err)

	r, err := e.runs.Create(dave, tk.Key, "implement", run.Spec{Command: []string{"make", "test"}, Env: map[string]string{"MODE": "run"}})
	require.NoError(t, err)
	assert.Equal(t, "ballet/"+tk.Key+"-export-invoices", r.Branch)
	assert.Equal(t, map[string]string{"CI": "1", "MODE": "run", "BALLET_TICKET": tk.Key, "BALLET_STAGE": "implement",
		"BALLET_BRANCH": r.Branch}, r.Spec.Env)
	assert.Equal(t, []string{"sh", "-c"}, r.Spec.Command[:2])
	assert.Equal(t, []string{"ballet-workspace", "make", "test"}, r.Spec.Command[3:])

	fr := &fakeAgent{}
	require.NoError(t, e.d.Connect(t.Context(), app.AgentInfo{Name: "r1", Capacity: 1}, fr))
	e.eventually(t, r.ID, run.StatusStarting)
	fr.mu.Lock()
	assert.Equal(t, map[string]string{execution.TokenEnv: "s3cret"}, fr.secrets[0], "the token goes to the Runner")
	fr.mu.Unlock()
	stored, _ := json.Marshal(e.status(t, r.ID))
	assert.False(t, strings.Contains(string(stored), "s3cret"), "and is not stored with the run")
}

func TestRuns_AgentSessionsWorkInThePreparedRepository(t *testing.T) {
	e := newRuns(t, time.Minute)
	ex := &app.Execution{Store: e.st, Tenancy: e.st, Authz: e.runs.Authz, Now: time.Now}
	e.runs.Execution = e.st
	dave := user(t, "dave", "acme-admins")
	_, err := ex.Set(dave, "WEB", execution.Settings{RepoURL: "https://github.com/acme/web.git", DefaultBranch: "main"}, 0)
	require.NoError(t, err)
	tk, err := e.tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Export"})
	require.NoError(t, err)

	r, err := e.runs.CreateAgent(dave, tk.Key, "implement", app.AgentInput{Adapter: "claude-code", Prompt: "p"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ballet-workspace", "true"}, r.Spec.Command[3:], "the command only prepares the workspace")
	assert.Equal(t, execution.RepoDir, r.Spec.Session.Dir, "the session works in the clone")
}

func TestRuns_GoToTheProjectsAgentPool(t *testing.T) {
	e := newRuns(t, time.Minute)
	ex := &app.Execution{Store: e.st, Tenancy: e.st, Authz: e.runs.Authz, Now: time.Now}
	e.runs.Execution = e.st
	dave := user(t, "dave", "acme-admins")
	_, err := ex.Set(dave, "WEB", execution.Settings{Pool: "Web Shop"}, 0)
	assert.ErrorIs(t, err, app.ErrInvalid)
	v, err := ex.Set(dave, "WEB", execution.Settings{Pool: "web-shop"}, 0)
	require.NoError(t, err)
	assert.Equal(t, "web-shop", v.Pool)
	tk, err := e.tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "x"})
	require.NoError(t, err)
	r, err := e.runs.CreateAgent(dave, tk.Key, "implement", app.AgentInput{Adapter: "claude-code", Prompt: "p"})
	require.NoError(t, err)
	assert.Equal(t, "web-shop", r.Spec.Pool)
}
