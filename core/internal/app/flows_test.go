package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/agent"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

// scriptedRunner finishes every run it gets with a stage report whose
// outcome the script decides.
type scriptedRunner struct {
	d       *app.Dispatcher
	st      *store.Store
	mu      sync.Mutex
	stages  []string
	prompts map[string]string
	script  func(stage string, n int) report.Outcome // n: how often this stage ran so far (1-based)
	hold    bool                                     // do not finish runs
	held    []run.Run
	// ask, when set, is called before a run reports (to raise questions).
	ask func(r run.Run, n int)
}

func (s *scriptedRunner) Start(_ context.Context, r run.Run, _ map[string]string) error {
	s.mu.Lock()
	s.stages = append(s.stages, r.Stage)
	n := 0
	for _, x := range s.stages {
		if x == r.Stage {
			n++
		}
	}
	if r.Spec.Session != nil {
		s.prompts[r.Stage+"#"+string(rune('0'+n))] = r.Spec.Session.Prompt
	}
	hold := s.hold
	if hold {
		s.held = append(s.held, r)
	}
	s.mu.Unlock()
	if hold {
		return nil
	}
	go func() {
		ctx := context.Background()
		_ = s.d.Running(ctx, "r1", r.ID)
		s.mu.Lock()
		ask := s.ask
		s.mu.Unlock()
		if ask != nil {
			ask(r, n)
		}
		outcome := s.script(r.Stage, n)
		_ = s.st.CreateReport(ctx, report.Report{ID: store.NewID(), ProjectID: r.ProjectID, TicketID: r.TicketID, RunID: r.ID,
			Kind: report.KindStageReport, Outcome: outcome, Text: r.Stage + " " + string(outcome) + " #" + string(rune('0'+n)),
			CreatedAt: time.Now()}, eventFor(r))
		_ = s.d.Finished(ctx, "r1", r.ID, 0, "", false, &app.SessionResult{Success: true, Summary: "done"})
	}()
	return nil
}

func (s *scriptedRunner) Cancel(ctx context.Context, runID string) error {
	go func() { _ = s.d.Finished(context.Background(), "r1", runID, -1, "", true, nil) }()
	return nil
}

func (s *scriptedRunner) ran() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stages...)
}

type flowEnv struct {
	flows  *app.Flows
	tr     *app.Tracker
	runner *scriptedRunner
	forge  *memForge
	st     *store.Store
}

// newFlows builds a flow environment; setup runs before the orchestrator
// and dispatcher start (to simulate failures).
func newFlows(t *testing.T, script func(string, int) report.Outcome, setup ...func(*app.Orchestrator, *app.Dispatcher, *app.Flows)) flowEnv {
	t.Helper()
	tr, env := newTracker(t)
	st := env.store
	o := &app.Orchestrator{Store: st, Now: time.Now, Interval: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 20 * time.Millisecond }}
	d := &app.Dispatcher{Store: st, Tenancy: st, Now: time.Now, Interval: 10 * time.Millisecond, MaxLogBytes: 1 << 20}
	runs := &app.Runs{Store: st, Execution: st, Items: st, Tenancy: st, Authz: env.rbac, Dispatcher: d,
		MCP:  []agent.MCPServer{{Name: "tracker"}},
		Deps: st, Now: time.Now, NewID: store.NewID}
	mf := &memForge{prs: map[string]forge.PullRequest{}}
	prs := &app.PullRequests{Store: st, Execution: st, Items: st, Tenancy: st, Authz: env.rbac,
		Forges: func(execution.Settings) (forge.Forge, error) { return mf, nil },
		Token:  func(context.Context, string, string) (string, error) { return "", nil }, Now: time.Now}
	fl := &app.Flows{Store: st, Items: st, Tenancy: st, Authz: env.rbac, RunStore: st, Reports: st, Runs: runs,
		Pipelines:    &app.Pipelines{Store: st, Tenancy: st, Authz: env.rbac, Adapters: []string{"claude-code"}, Now: time.Now},
		PullRequests: prs, Orchestrator: o, Now: time.Now, NewID: store.NewID, CheckInterval: 30 * time.Millisecond}
	fl.Register(o)
	d.OnFinished = fl.RunFinished
	for _, f := range setup {
		f(o, d, fl)
	}
	sr := &scriptedRunner{d: d, st: st, prompts: map[string]string{}, script: script}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); o.Run(ctx) }()
	go func() { defer wg.Done(); d.Run(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
	require.NoError(t, d.Connect(t.Context(), app.RunnerInfo{Name: "r1", Capacity: 4}, sr))

	ex := &app.Execution{Store: st, Tenancy: st, Authz: env.rbac, Now: time.Now}
	_, err := ex.Set(user(t, "dave", "acme-admins"), "WEB", execution.Settings{RepoURL: "https://github.com/acme/web.git",
		DefaultBranch: "main"}, 0)
	require.NoError(t, err)
	return flowEnv{flows: fl, tr: tr, runner: sr, forge: mf, st: st}
}

func (e flowEnv) ticket(t *testing.T, policy tracker.Policy) app.ItemView {
	t.Helper()
	dave := user(t, "dave", "acme-admins")
	it, err := e.tr.CreateItem(dave, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Login",
		AcceptanceCriteria: []string{"works"}, Policy: &policy})
	require.NoError(t, err)
	it, err = e.tr.TransitionItem(dave, it.Key, tracker.StateReady, it.Version)
	require.NoError(t, err)
	return it
}

func (e flowEnv) waitFlow(t *testing.T, key string, ok func(app.FlowView) bool) app.FlowView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := e.flows.Get(user(t, "bob", "acme-devs"), key)
		if err == nil && ok(f) {
			return f
		}
		if time.Now().After(deadline) {
			t.Fatalf("flow never reached the expected state: err=%v stage=%s iteration=%d status=%s waiting=%s run=%s outcome=%s ran=%v",
				err, f.Stage, f.Iteration, f.Status, f.Waiting, f.RunID, f.Outcome, e.runner.ran())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e flowEnv) state(t *testing.T, key string) tracker.State {
	t.Helper()
	it, err := e.tr.GetItem(user(t, "bob", "acme-devs"), key)
	require.NoError(t, err)
	return it.State
}

var auto = tracker.Policy{ReviewMode: tracker.ReviewAgent, MergeMode: tracker.MergeAuto}

func always(o report.Outcome) func(string, int) report.Outcome {
	return func(string, int) report.Outcome { return o }
}

func TestFlows_DefaultPipelineRunsUnattended(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	e.forge.checks = forge.ChecksSuccess
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "bob", "acme-devs"), tk.Key)
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers do not start pipelines by hand")
	f, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	assert.Equal(t, "implement", f.Stage)
	_, err = e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	assert.ErrorIs(t, err, app.ErrConflict)

	done := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	assert.Equal(t, []string{"implement", "review", "verify"}, e.runner.ran(), "a separate session per agent stage")
	assert.Equal(t, tracker.StateDone, e.state(t, tk.Key))
	assert.Contains(t, done.Report, "Merged: https://forge/pr")
	assert.Equal(t, []int{1}, e.forge.merged, "auto merge")
	e.runner.mu.Lock()
	assert.Contains(t, e.runner.prompts["review#1"], "Report of the implement stage (done):\n\nimplement done #1",
		"stages hand over their reports")
	e.runner.mu.Unlock()
}

func TestFlows_FailedReviewLoopsBackUntilTheLimit(t *testing.T) {
	e := newFlows(t, func(stage string, n int) report.Outcome {
		if stage == "review" && n == 1 {
			return report.OutcomeFailed
		}
		return report.OutcomeDone
	})
	e.forge.checks = forge.ChecksSuccess
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	done := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	assert.Equal(t, []string{"implement", "review", "implement", "review", "verify"}, e.runner.ran())
	assert.Equal(t, 1, done.Iteration)
	e.runner.mu.Lock()
	assert.Contains(t, e.runner.prompts["implement#2"], "Report of the review stage (failed)", "findings go back to implement")
	e.runner.mu.Unlock()

	// A review that always fails exceeds the iteration limit and asks.
	e2 := newFlows(t, func(stage string, _ int) report.Outcome {
		if stage == "review" {
			return report.OutcomeFailed
		}
		return report.OutcomeDone
	})
	tk2 := e2.ticket(t, auto)
	_, err = e2.flows.Start(user(t, "dave", "acme-admins"), tk2.Key)
	require.NoError(t, err)
	w := e2.waitFlow(t, tk2.Key, func(f app.FlowView) bool { return f.Waiting == "question" })
	assert.Equal(t, "implement", w.Stage, "after the answer it goes on where the loop went")
	assert.Equal(t, tracker.StateWaitingForAnswer, e2.state(t, tk2.Key))
	it, _ := e2.st.ItemByKey(t.Context(), tk2.Key)
	qs, err := e2.st.Questions(t.Context(), it.ID)
	require.NoError(t, err)
	require.Len(t, qs, 1)
	assert.True(t, qs[0].Blocking)
	assert.Contains(t, qs[0].Context, "more than its limit of 3")
	assert.Len(t, e2.runner.ran(), 8, "implement and review four times each")
}

func TestFlows_ManualMergeWaitsForAHuman(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	tk := e.ticket(t, tracker.Policy{ReviewMode: tracker.ReviewAgentHuman, MergeMode: tracker.MergeManual})
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "checks" })

	e.forge.set("ballet/"+tk.Key+"-login", func(p *forge.PullRequest) { p.Checks = forge.ChecksSuccess })
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "review" })
	e.forge.set("ballet/"+tk.Key+"-login", func(p *forge.PullRequest) { p.Review = forge.ReviewApproved })
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "merge" })
	assert.Empty(t, e.forge.merged, "manual merge: Ballet does not merge")

	e.forge.set("ballet/"+tk.Key+"-login", func(p *forge.PullRequest) { p.State = forge.StateMerged })
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	assert.Equal(t, tracker.StateDone, e.state(t, tk.Key))
}

func TestFlows_FailingChecksGoBackToImplement(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	e.forge.mu.Lock()
	e.forge.prs["ballet/WEB-1-login"] = forge.PullRequest{Number: 9, Head: "ballet/WEB-1-login", State: forge.StateOpen,
		Checks: forge.ChecksFailure, Review: forge.ReviewNone}
	e.forge.mu.Unlock()
	tk := e.ticket(t, auto)
	require.Equal(t, "WEB-1", tk.Key)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	// Checks keep failing, so the loop ends at the iteration limit.
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "question" })
	assert.Equal(t, []string{"implement", "review", "verify", "implement"}, e.runner.ran()[:4])
	e.runner.mu.Lock()
	defer e.runner.mu.Unlock()
	assert.Contains(t, e.runner.prompts["implement#2"], "Checks failed", "check failures go back to implement")
}

func TestFlows_HumanStagesAndStopping(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	dave := user(t, "dave", "acme-admins")
	p, _ := e.st.ProjectByKey(t.Context(), "WEB")
	_ = p
	ps := e.flows.Pipelines
	_, err := ps.Save(dave, "WEB", "default", pipeline.Definition{MaxIterations: 1, Stages: []pipeline.Stage{
		{ID: "build", Kind: pipeline.KindAgent},
		{ID: "approve", Kind: pipeline.KindHuman, Next: map[pipeline.Outcome]string{pipeline.OutcomeFailed: pipeline.TargetFailed}},
	}}, 0)
	require.NoError(t, err)

	tk := e.ticket(t, auto)
	_, err = e.flows.Start(dave, tk.Key)
	require.NoError(t, err)
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "approval" })
	_, err = e.flows.Decide(user(t, "carol", "acme-viewers"), tk.Key, true, "")
	assert.ErrorIs(t, err, app.ErrForbidden)
	f, err := e.flows.Decide(user(t, "bob", "acme-devs"), tk.Key, false, "Not what we wanted.")
	require.NoError(t, err)
	assert.Equal(t, app.FlowFailed, f.Status)
	assert.Contains(t, f.Report, "Not what we wanted.")
	assert.Equal(t, tracker.StatePaused, e.state(t, tk.Key))

	// A human pauses a ticket mid-stage: its run is cancelled, the flow stops.
	e.runner.mu.Lock()
	e.runner.hold = true
	e.runner.mu.Unlock()
	tk2 := e.ticket(t, auto)
	_, err = e.flows.Start(dave, tk2.Key)
	require.NoError(t, err)
	running := e.waitFlow(t, tk2.Key, func(f app.FlowView) bool { return f.RunID != "" })
	it, err := e.tr.GetItem(dave, tk2.Key)
	require.NoError(t, err)
	_, err = e.tr.TransitionItem(dave, tk2.Key, tracker.StatePaused, it.Version)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		r, _ := e.st.Run(t.Context(), running.RunID)
		return r.Status == run.StatusStarting || r.Status == run.StatusRunning
	}, 5*time.Second, 10*time.Millisecond)
	e.runner.mu.Lock()
	held := e.runner.held[len(e.runner.held)-1]
	e.runner.mu.Unlock()
	require.NoError(t, e.runner.d.Finished(t.Context(), "r1", held.ID, 0, "", false, nil))
	stopped := e.waitFlow(t, tk2.Key, func(f app.FlowView) bool { return f.Status == app.FlowStopped })
	assert.Equal(t, "build", stopped.Stage)
}

func eventFor(r run.Run) event.Event {
	return event.Event{Project: r.ProjectID, EntityType: "item", EntityID: r.TicketID, Type: "item.report_added",
		Actor: event.Actor{Kind: event.ActorService, Subject: "run:" + r.ID}, OccurredAt: time.Now()}
}

func TestFlows_NothingPushedGoesBackToImplement(t *testing.T) {
	e := newFlows(t, always(report.OutcomeDone))
	e.forge.noBranch = true
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "question" })
	assert.Equal(t, []string{"implement", "review", "verify", "implement"}, e.runner.ran()[:4])
	e.runner.mu.Lock()
	defer e.runner.mu.Unlock()
	assert.Contains(t, e.runner.prompts["implement#2"], "was not pushed", "a missing branch goes back to implement")
}
