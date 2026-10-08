package app_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/agent"
	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

// fakeRunner records what Core asks of it.
type fakeRunner struct {
	mu        sync.Mutex
	started   []string
	cancelled []string
	refuse    bool
	attempts  int
	secrets   []map[string]string
}

func (f *fakeRunner) Start(_ context.Context, r run.Run, secrets map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	f.secrets = append(f.secrets, secrets)
	if f.refuse {
		return errors.New("busy")
	}
	f.started = append(f.started, r.ID)
	return nil
}

func (f *fakeRunner) Cancel(_ context.Context, runID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, runID)
	return nil
}

func (f *fakeRunner) starts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

type runsEnv struct {
	runs *app.Runs
	d    *app.Dispatcher
	tr   *app.Tracker
	st   *store.Store
}

func newRuns(t *testing.T, grace time.Duration) runsEnv {
	t.Helper()
	tr, env := newTracker(t)
	d := &app.Dispatcher{Store: env.store, Tenancy: env.store, Now: time.Now, Grace: grace, MaxLogBytes: 20,
		Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return runsEnv{
		runs: &app.Runs{Store: env.store, Items: env.store, Tenancy: env.store, Authz: env.rbac, Dispatcher: d,
			Now: time.Now, NewID: store.NewID},
		d: d, tr: tr, st: env.store,
	}
}

var cmd = run.Spec{Command: []string{"make", "test"}}

func (e runsEnv) status(t *testing.T, id string) run.Run {
	t.Helper()
	r, err := e.st.Run(t.Context(), id)
	require.NoError(t, err)
	return r
}

func (e runsEnv) eventually(t *testing.T, id string, want run.Status) run.Run {
	t.Helper()
	require.Eventually(t, func() bool { return e.status(t, id).Status == want }, 5*time.Second, 5*time.Millisecond,
		"run %s never became %s", id, want)
	return e.status(t, id)
}

func TestRuns_CreateIsForRunManagersOnTickets(t *testing.T) {
	e := newRuns(t, time.Minute)
	dave := user(t, "dave", "acme-admins")
	bob := user(t, "bob", "acme-devs")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	ep := mk(t, e.tr, tracker.KindEpic, "e")

	_, err := e.runs.Create(bob, tk.Key, "implement", cmd)
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers do not queue runs by hand")
	_, err = e.runs.Create(dave, ep.Key, "implement", cmd)
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = e.runs.Create(dave, tk.Key, "Implement!", cmd)
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = e.runs.Create(dave, tk.Key, "implement", run.Spec{})
	assert.ErrorIs(t, err, app.ErrInvalid)

	r, err := e.runs.Create(dave, tk.Key, "implement", cmd)
	require.NoError(t, err)
	assert.Equal(t, run.StatusQueued, r.Status)
	list, err := e.runs.ListForTicket(bob, tk.Key)
	require.NoError(t, err)
	assert.Len(t, list, 1, "engineers read runs")
}

func TestRuns_DispatchRespectsCapacityAndRecordsReports(t *testing.T) {
	e := newRuns(t, time.Minute)
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	a, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	b, _ := e.runs.Create(dave, tk.Key, "review", cmd)

	fr := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, fr))
	assert.ErrorIs(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, &fakeRunner{}), app.ErrRunnerConflict)
	e.eventually(t, a.ID, run.StatusStarting)
	assert.Equal(t, []string{a.ID}, fr.starts())
	assert.Equal(t, run.StatusQueued, e.status(t, b.ID).Status, "capacity 1")

	require.NoError(t, e.d.Running(t.Context(), "r1", a.ID))
	require.NoError(t, e.d.Log(t.Context(), "r1", a.ID, "stdout", "ok 1\n"))
	require.NoError(t, e.d.Log(t.Context(), "r1", a.ID, "stdout", "ok 2\n"))
	require.NoError(t, e.d.Log(t.Context(), "r1", a.ID, "stdout", "this chunk exceeds the log limit\n"))
	logs, err := e.runs.Logs(dave, a.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, logs, 2, "output beyond MaxLogBytes is dropped")
	assert.Equal(t, "ok 2\n", logs[1].Text)
	later, _ := e.runs.Logs(dave, a.ID, logs[0].Seq, 0)
	assert.Len(t, later, 1)

	assert.ErrorIs(t, e.d.Log(t.Context(), "r2", a.ID, "stdout", "x"), app.ErrForbidden, "only the run's runner reports")
	require.NoError(t, e.d.Finished(t.Context(), "r1", a.ID, 0, "", false))
	done := e.status(t, a.ID)
	assert.Equal(t, run.StatusSucceeded, done.Status)
	assert.Equal(t, 0, *done.ExitCode)
	assert.False(t, done.StartedAt.IsZero())
	assert.ErrorIs(t, e.d.Finished(t.Context(), "r1", a.ID, 0, "", false), app.ErrForbidden, "finished runs take no reports")

	e.eventually(t, b.ID, run.StatusStarting)
	require.NoError(t, e.d.Finished(t.Context(), "r1", b.ID, 2, "", false))
	failed := e.status(t, b.ID)
	assert.Equal(t, run.StatusFailed, failed.Status)
	assert.Equal(t, "exited with code 2", failed.Error)
}

func TestRuns_CancelQueuedAndActive(t *testing.T) {
	e := newRuns(t, time.Minute)
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	q, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	got, err := e.runs.Cancel(dave, q.ID)
	require.NoError(t, err)
	assert.Equal(t, run.StatusCancelled, got.Status)
	_, err = e.runs.Cancel(dave, q.ID)
	assert.ErrorIs(t, err, app.ErrConflict)

	fr := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 2}, fr))
	a, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	e.eventually(t, a.ID, run.StatusStarting)
	_, err = e.runs.Cancel(dave, a.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{a.ID}, fr.cancelled, "the runner is asked to stop")
	require.NoError(t, e.d.Finished(t.Context(), "r1", a.ID, -1, "", true))
	assert.Equal(t, run.StatusCancelled, e.status(t, a.ID).Status)
}

func TestRuns_RefusedRunsAreRequeued(t *testing.T) {
	e := newRuns(t, time.Minute)
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	r, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	fr := &fakeRunner{refuse: true}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, fr))
	require.Eventually(t, func() bool { fr.mu.Lock(); defer fr.mu.Unlock(); return fr.attempts >= 2 }, 5*time.Second,
		5*time.Millisecond, "a refused run goes back to the queue and is offered again")
	assert.Empty(t, fr.starts())

	fr.mu.Lock()
	fr.refuse = false
	fr.mu.Unlock()
	require.Eventually(t, func() bool { return len(fr.starts()) == 1 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{r.ID}, fr.starts())
	got := e.status(t, r.ID)
	assert.Equal(t, run.StatusStarting, got.Status, "accepted runs stay with their runner")
	assert.Equal(t, "r1", got.Runner)
}

func TestRuns_ReconnectAndDisconnect(t *testing.T) {
	e := newRuns(t, 100*time.Millisecond)
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")
	a, _ := e.runs.Create(dave, tk.Key, "implement", cmd)
	b, _ := e.runs.Create(dave, tk.Key, "review", cmd)
	first := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 2}, first))
	e.eventually(t, a.ID, run.StatusStarting)
	e.eventually(t, b.ID, run.StatusStarting)

	// The runner reconnects still executing a, but lost b.
	e.d.Disconnect("r1", first)
	second := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 2, Active: []string{a.ID}}, second))
	assert.Equal(t, run.StatusStarting, e.status(t, a.ID).Status)
	assert.Equal(t, run.StatusFailed, e.status(t, b.ID).Status)
	assert.Len(t, e.d.Runners(), 1)

	// Gone for longer than Grace: its runs fail.
	e.d.Disconnect("r1", second)
	failed := e.eventually(t, a.ID, run.StatusFailed)
	assert.Equal(t, "the agent disconnected", failed.Error)
}

// echoAgent builds a session that prints its prompt; results are lines
// "RESULT <ok|fail> <summary>".
type echoAgent struct{}

func (echoAgent) Name() string { return "echo" }

func (echoAgent) Build(s agent.Session) (agent.Built, error) {
	return agent.Built{Command: []string{"cat", ".ballet/prompt"}, Files: map[string]string{".ballet/prompt": s.Prompt},
		Env: map[string]string{"SKILLS": fmt.Sprint(len(s.Skills)), "MCP": s.MCP[0].Name}}, nil
}

func (echoAgent) Result(stdout string) (agent.Result, bool) {
	i := strings.LastIndex(stdout, "RESULT ")
	if i < 0 {
		return agent.Result{}, false
	}
	f := strings.SplitN(strings.TrimSpace(stdout[i+7:]), " ", 2)
	return agent.Result{Success: f[0] == "ok", Summary: f[1], Turns: 2}, true
}

func TestRuns_AgentRunsAreBuiltByTheirAdapterAndJudgedByTheirResult(t *testing.T) {
	e := newRuns(t, time.Minute)
	e.runs.Agents = map[string]agent.Adapter{"echo": echoAgent{}}
	e.d.Adapters = e.runs.Agents
	e.runs.MCP = []agent.MCPServer{{Name: "knowledge", URL: "http://kn/mcp", TokenEnv: "T"}}
	e.runs.SessionSkills = func(context.Context, string) ([]agent.Skill, error) {
		return []agent.Skill{{Name: "gitflow"}}, nil
	}
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "t")

	_, err := e.runs.CreateAgent(dave, tk.Key, "implement", app.AgentInput{Adapter: "nope", Prompt: "x"})
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = e.runs.CreateAgent(user(t, "bob", "acme-devs"), tk.Key, "implement", app.AgentInput{Adapter: "echo", Prompt: "x"})
	assert.ErrorIs(t, err, app.ErrForbidden)

	r, err := e.runs.CreateAgent(dave, tk.Key, "implement", app.AgentInput{Adapter: "echo", Prompt: "Do it", TimeoutSeconds: 60})
	require.NoError(t, err)
	assert.Equal(t, "echo", r.Adapter)
	assert.Contains(t, r.Spec.Files[".ballet/prompt"], "## Additional instructions\n\nDo it")
	assert.Equal(t, "1", r.Spec.Env["SKILLS"])
	assert.Equal(t, "knowledge", r.Spec.Env["MCP"])
	assert.Equal(t, 60, r.Spec.TimeoutSeconds)

	fr := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 3}, fr))
	finish := func(out string, code int) run.Run {
		t.Helper()
		v, err := e.runs.CreateAgent(dave, tk.Key, "implement", app.AgentInput{Adapter: "echo", Prompt: "p"})
		require.NoError(t, err)
		e.eventually(t, v.ID, run.StatusStarting)
		if out != "" {
			require.NoError(t, e.d.Log(t.Context(), "r1", v.ID, "stdout", out))
		}
		require.NoError(t, e.d.Finished(t.Context(), "r1", v.ID, code, "", false))
		return e.status(t, v.ID)
	}
	e.d.MaxLogBytes = 1 << 20

	ok := finish("working\nRESULT ok Added login.\n", 0)
	assert.Equal(t, run.StatusSucceeded, ok.Status)
	assert.Equal(t, &run.Result{Summary: "Added login.", Turns: 2}, ok.Result)

	failed := finish("RESULT fail Tests do not pass.\n", 0)
	assert.Equal(t, run.StatusFailed, failed.Status, "the agent's own verdict counts")
	assert.Equal(t, "the agent reported a failure: Tests do not pass.", failed.Error)

	silent := finish("crashed\n", 0)
	assert.Equal(t, run.StatusFailed, silent.Status)
	assert.Equal(t, "the agent reported no result", silent.Error)
}

func TestRuns_AgentPromptIsTheOnboardingBundle(t *testing.T) {
	e := newRuns(t, time.Minute)
	e.runs.Agents = map[string]agent.Adapter{"echo": echoAgent{}}
	e.runs.MCP = []agent.MCPServer{{Name: "knowledge"}}
	e.runs.Deps = e.st
	var asked []string
	e.runs.Knowledge = func(_ context.Context, customer, project, ticket, query string, limit int) ([]onboarding.Knowledge, error) {
		asked = append(asked, customer, project, ticket, query)
		return []onboarding.Knowledge{{ID: "k1", Kind: "decision", Title: "Use OIDC", Body: "Keycloak.", Linked: true}}, nil
	}
	dave := user(t, "dave", "acme-admins")
	epic := mk(t, e.tr, tracker.KindEpic, "Auth")
	store := mk(t, e.tr, tracker.KindTicket, "Session store")
	login, err := e.tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Login",
		Description: "Sign in with OIDC.", AcceptanceCriteria: []string{"Works"}, EpicKey: epic.Key})
	require.NoError(t, err)
	_, err = e.tr.AddDependency(dave, store.Key, app.DirBlocks, login.Key)
	require.NoError(t, err)

	// The blocker was implemented by an earlier agent run.
	e.d.Adapters = e.runs.Agents
	fr := &fakeRunner{}
	require.NoError(t, e.d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 1}, fr))
	prev, err := e.runs.CreateAgent(dave, store.Key, "implement", app.AgentInput{Adapter: "echo", Prompt: "p"})
	require.NoError(t, err)
	e.eventually(t, prev.ID, run.StatusStarting)
	e.d.MaxLogBytes = 1 << 20
	require.NoError(t, e.d.Log(t.Context(), "r1", prev.ID, "stdout", "RESULT ok Added a Redis session store.\n"))
	require.NoError(t, e.d.Finished(t.Context(), "r1", prev.ID, 0, "", false))

	r, err := e.runs.CreateAgent(dave, login.Key, "implement", app.AgentInput{Adapter: "echo", Prompt: "Keep it small."})
	require.NoError(t, err)
	prompt := r.Spec.Files[".ballet/prompt"]
	for _, want := range []string{
		"# " + login.Key + ": Login", "**implement** stage", "Sign in with OIDC.", "- [ ] Works",
		"### Epic " + epic.Key + ": Auth", "## Depends on " + store.Key + ": Session store",
		"implement run (succeeded): Added a Redis session store.", "## Knowledge (decision, linked to this ticket): Use OIDC",
		"Keep it small.",
	} {
		assert.Contains(t, prompt, want)
	}
	assert.Equal(t, []string{"acme", "WEB", login.Key, "Login"}, asked[len(asked)-4:], "knowledge for the ticket")
}
