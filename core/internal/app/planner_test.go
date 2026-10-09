package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/kit/auth"
)

// fakeLLM answers with scripted responses in order; a nil response blocks
// until the request is cancelled.
type fakeLLM struct {
	mu        sync.Mutex
	responses []*app.LLMResponse
	requests  []app.LLMRequest
}

func (f *fakeLLM) script(rs ...*app.LLMResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = append(f.responses, rs...)
}

func (f *fakeLLM) Stream(ctx context.Context, req app.LLMRequest, onText func(string)) (app.LLMResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	if len(f.responses) == 0 {
		f.mu.Unlock()
		return app.LLMResponse{}, errors.New("fake LLM: no scripted response")
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	f.mu.Unlock()
	if r == nil {
		<-ctx.Done()
		return app.LLMResponse{}, ctx.Err()
	}
	for _, b := range r.Content {
		if b.Type == planner.BlockText {
			onText(b.Text)
		}
	}
	return *r, nil
}

func (f *fakeLLM) request(i int) app.LLMRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func say(text string) *app.LLMResponse {
	return &app.LLMResponse{Content: []planner.Block{planner.Text(text)}, StopReason: "end_turn",
		Usage: planner.Usage{InputTokens: 10, OutputTokens: 2}}
}

func useTool(id, name, input string) *app.LLMResponse {
	return &app.LLMResponse{StopReason: "tool_use", Content: []planner.Block{
		planner.Text("Let me check."),
		{Type: planner.BlockToolUse, ToolUseID: id, Name: name, Input: json.RawMessage(input)},
	}}
}

// whoami returns the subject of the identity the tool runs as.
type whoami struct{}

func (whoami) Spec() app.ToolSpec {
	return app.ToolSpec{Name: "whoami", Description: "Who the planner acts for", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func (whoami) Call(ctx context.Context, env app.ToolEnv, _ json.RawMessage) (string, error) {
	id, _ := auth.FromContext(ctx)
	return id.Subject + "@" + env.ProjectKey, nil
}

type failing struct{}

func (failing) Spec() app.ToolSpec { return app.ToolSpec{Name: "fail"} }
func (failing) Call(context.Context, app.ToolEnv, json.RawMessage) (string, error) {
	return "", app.ErrForbidden
}

func newPlanner(t *testing.T, llm *fakeLLM, st *store.Store, env rbacEnv) *app.Planner {
	t.Helper()
	return &app.Planner{
		Store: st, Tenancy: st, Authz: env.rbac, LLM: llm, Tools: []app.PlannerTool{whoami{}, failing{}},
		Instructions: func(context.Context, string) (string, error) { return "Use gitflow.", nil },
		Model:        "claude-test", MaxTokens: 1000, MaxRounds: 3, Now: time.Now, NewID: store.NewID, Context: t.Context(),
	}
}

// collect watches a session and returns a function waiting for the turn's
// done output, returning everything received.
func collect(t *testing.T, pl *app.Planner, ctx context.Context, session string) func() []app.PlannerOutput {
	t.Helper()
	var mu sync.Mutex
	var out []app.PlannerOutput
	done := make(chan struct{}, 8)
	w, _, err := pl.Watch(ctx, session, func(o app.PlannerOutput) {
		mu.Lock()
		out = append(out, o)
		mu.Unlock()
		if o.Type == app.OutputDone {
			done <- struct{}{}
		}
	})
	require.NoError(t, err)
	t.Cleanup(w.Close)
	return func() []app.PlannerOutput {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("turn did not finish")
		}
		mu.Lock()
		defer mu.Unlock()
		res := out
		out = nil
		return res
	}
}

func types(out []app.PlannerOutput) []string {
	var ts []string
	for _, o := range out {
		ts = append(ts, o.Type)
	}
	return ts
}

func TestPlanner_ChatTurnStreamsAndPersists(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	llm := &fakeLLM{}
	pl := newPlanner(t, llm, env.store, env)
	bob := user(t, "bob", "acme-devs")

	s, err := pl.CreateSession(bob, "WEB", "Auth planning")
	require.NoError(t, err)
	wait := collect(t, pl, bob, s.ID)
	llm.script(say("Hello Bob."))

	m, err := pl.Send(bob, s.ID, "Plan authentication")
	require.NoError(t, err)
	assert.Equal(t, int64(1), m.Seq)
	out := wait()
	assert.Equal(t, []string{app.OutputMessage, app.OutputText, app.OutputMessage, app.OutputDone}, types(out))
	assert.Equal(t, "Hello Bob.", out[1].Text)
	assert.Equal(t, "end_turn", out[3].Text)

	view, msgs, err := pl.Session(bob, s.ID)
	require.NoError(t, err)
	assert.False(t, view.Running)
	require.Len(t, msgs, 2)
	assert.Equal(t, "bob", msgs[0].Author)
	assert.Equal(t, planner.RoleAssistant, msgs[1].Role)
	assert.Equal(t, int64(10), msgs[1].Usage.InputTokens)

	req := llm.request(0)
	assert.Equal(t, "claude-test", req.Model)
	assert.Contains(t, req.System, "planner of a software project")
	assert.Contains(t, req.System, "Use gitflow.")
	assert.Equal(t, app.LLMCaller{OrganizationKey: "acme", ProjectKey: "WEB", SessionID: s.ID, ActingFor: "bob"}, req.Caller)
	assert.Len(t, req.Tools, 2)
}

func TestPlanner_ToolCallsRunAsTheHumanUntilTheModelStops(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	llm := &fakeLLM{}
	pl := newPlanner(t, llm, env.store, env)
	bob := user(t, "bob", "acme-devs")
	s, err := pl.CreateSession(bob, "WEB", "t")
	require.NoError(t, err)
	wait := collect(t, pl, bob, s.ID)

	llm.script(useTool("t1", "whoami", `{}`), &app.LLMResponse{StopReason: "tool_use", Content: []planner.Block{
		{Type: planner.BlockToolUse, ToolUseID: "t2", Name: "fail", Input: json.RawMessage(`{}`)},
		{Type: planner.BlockToolUse, ToolUseID: "t3", Name: "nope"},
	}}, say("Done."))
	_, err = pl.Send(bob, s.ID, "go")
	require.NoError(t, err)
	out := wait()
	assert.Contains(t, types(out), app.OutputToolCall)

	_, msgs, err := pl.Session(bob, s.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 6, "user, tool_use, results, tool_use, results, answer")
	first := msgs[2].Content[0]
	assert.Equal(t, planner.BlockToolResult, first.Type)
	assert.Equal(t, "bob@WEB", first.Text, "tools act for the human, in the session's project")
	assert.False(t, first.IsError)
	assert.True(t, msgs[4].Content[0].IsError)
	assert.Contains(t, msgs[4].Content[0].Text, "forbidden")
	assert.Contains(t, msgs[4].Content[1].Text, `unknown tool "nope"`)

	second := llm.request(1)
	last := second.Messages[len(second.Messages)-1]
	assert.Equal(t, planner.RoleUser, last.Role)
	assert.Equal(t, "t1", last.Content[0].ToolUseID, "the model sees the tool result")
}

func TestPlanner_MaxRoundsBoundsATurn(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	llm := &fakeLLM{}
	pl := newPlanner(t, llm, env.store, env)
	bob := user(t, "bob", "acme-devs")
	s, _ := pl.CreateSession(bob, "WEB", "t")
	wait := collect(t, pl, bob, s.ID)
	for range 5 {
		llm.script(useTool("t", "whoami", `{}`))
	}
	_, err := pl.Send(bob, s.ID, "loop")
	require.NoError(t, err)
	out := wait()
	assert.Equal(t, "max_rounds", out[len(out)-1].Text)
	assert.Len(t, llm.requests, 4, "MaxRounds 3: the first call plus three tool rounds")
}

func TestPlanner_CancelBusyAndRestart(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	llm := &fakeLLM{}
	pl := newPlanner(t, llm, env.store, env)
	bob := user(t, "bob", "acme-devs")
	s, _ := pl.CreateSession(bob, "WEB", "t")
	wait := collect(t, pl, bob, s.ID)

	llm.script(nil) // blocks until cancelled
	_, err := pl.Send(bob, s.ID, "first")
	require.NoError(t, err)
	_, err = pl.Send(bob, s.ID, "second")
	assert.ErrorIs(t, err, app.ErrConflict, "one turn at a time")
	require.Eventually(t, func() bool { v, _, _ := pl.Session(bob, s.ID); return v.Running }, time.Second, 5*time.Millisecond)
	require.NoError(t, pl.Cancel(bob, s.ID))
	out := wait()
	assert.Equal(t, "cancelled", out[len(out)-1].Text)

	// A new Planner over the same store (Core restarted) continues the
	// conversation from the persisted transcript.
	restarted := newPlanner(t, llm, env.store, env)
	wait2 := collect(t, restarted, bob, s.ID)
	llm.script(say("Back."))
	_, err = restarted.Send(bob, s.ID, "still there?")
	require.NoError(t, err)
	wait2()
	req := llm.request(1)
	require.Len(t, req.Messages, 1, "both user messages, merged")
	assert.Len(t, req.Messages[0].Content, 2)
	_, msgs, _ := restarted.Session(bob, s.ID)
	assert.Len(t, msgs, 3)
}

func TestPlanner_Authorization(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	llm := &fakeLLM{}
	pl := newPlanner(t, llm, env.store, env)
	bob := user(t, "bob", "acme-devs")
	carol := user(t, "carol", "acme-viewers")

	_, err := pl.CreateSession(carol, "WEB", "t")
	assert.ErrorIs(t, err, app.ErrForbidden)
	s, err := pl.CreateSession(bob, "WEB", " ")
	assert.ErrorIs(t, err, app.ErrInvalid)
	s, err = pl.CreateSession(bob, "WEB", "t")
	require.NoError(t, err)

	_, err = pl.Send(carol, s.ID, "hi")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, _, err = pl.Session(carol, s.ID)
	assert.NoError(t, err, "viewers can read")
	list, err := pl.ListSessions(carol, "WEB")
	require.NoError(t, err)
	assert.Len(t, list, 1)
	_, err = pl.Send(bob, s.ID, "")
	assert.ErrorIs(t, err, app.ErrInvalid)

	svc := &app.Planner{Store: env.store, Tenancy: env.store, Authz: fakeAuthz{app.ActTrackerWrite: {"*"}, app.ActTrackerRead: {"*"}},
		LLM: llm, Now: time.Now, NewID: store.NewID, Context: t.Context()}
	planner := auth.WithIdentity(context.Background(), auth.Identity{Kind: auth.KindService, Subject: "svc"})
	_, err = svc.Send(planner, s.ID, "hi")
	assert.ErrorIs(t, err, app.ErrForbidden, "only humans talk to the planner")
	_, err = pl.Send(user(t, "eve"), s.ID, "hi")
	assert.ErrorIs(t, err, app.ErrForbidden)
}

func TestPlanner_CompactsLongConversationsForTheModelOnly(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	llm := &fakeLLM{}
	pl := newPlanner(t, llm, env.store, env)
	pl.CompactAt = 50
	compactions := 0
	pl.OnCompact = func() { compactions++ }
	bob := user(t, "bob", "acme-devs")
	s, _ := pl.CreateSession(bob, "WEB", "t")
	wait := collect(t, pl, bob, s.ID)

	big := func(text string) *app.LLMResponse {
		r := say(text)
		r.Usage = planner.Usage{InputTokens: 40, CacheReadTokens: 20, OutputTokens: 5}
		return r
	}
	llm.script(big("First answer."))
	_, err := pl.Send(bob, s.ID, "first question")
	require.NoError(t, err)
	wait()
	assert.Zero(t, compactions, "the first call has nothing to compact")

	llm.script(say("SUMMARY: bob asked a first question."), big("Second answer."))
	_, err = pl.Send(bob, s.ID, "second question")
	require.NoError(t, err)
	out := wait()
	assert.Equal(t, 1, compactions)
	assert.Contains(t, types(out), app.OutputCompacted)

	summarize := llm.request(1)
	assert.Contains(t, summarize.System, "memory of a planning conversation")
	assert.Empty(t, summarize.Tools)
	main := llm.request(2)
	require.Len(t, main.Messages, 1, "summary and the new question, merged into one user message")
	assert.Contains(t, main.Messages[0].Content[0].Text, "SUMMARY: bob asked a first question.")
	assert.Equal(t, "second question", main.Messages[0].Content[1].Text)

	view, msgs, err := pl.Session(bob, s.ID)
	require.NoError(t, err)
	assert.Len(t, msgs, 4, "the transcript stays complete")
	assert.Equal(t, int64(2), view.SummaryUpTo)

	// A restarted planner keeps using the summary.
	restarted := newPlanner(t, llm, env.store, env)
	wait2 := collect(t, restarted, bob, s.ID)
	llm.script(say("Third."))
	_, err = restarted.Send(bob, s.ID, "third")
	require.NoError(t, err)
	wait2()
	third := llm.request(3)
	assert.Contains(t, third.Messages[0].Content[0].Text, "SUMMARY")
}

func TestPlanner_CompactionNeedsEarlierTurns(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	llm := &fakeLLM{}
	pl := newPlanner(t, llm, env.store, env)
	pl.CompactAt = 1
	bob := user(t, "bob", "acme-devs")
	s, _ := pl.CreateSession(bob, "WEB", "t")
	wait := collect(t, pl, bob, s.ID)
	llm.script(say("ok"))
	_, err := pl.Send(bob, s.ID, "a long first question that is over the threshold")
	require.NoError(t, err)
	out := wait()
	assert.NotContains(t, types(out), app.OutputCompacted, "the current turn is never summarized")
	assert.Len(t, llm.requests, 1)
}
