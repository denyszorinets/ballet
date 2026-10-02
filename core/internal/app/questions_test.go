package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

// memKnowledge is a knowledge base in memory.
type memKnowledge struct {
	mu       sync.Mutex
	entries  []onboarding.Knowledge
	recorded []string // "<ticket> <author>: <answer>"
}

func (k *memKnowledge) Search(_ context.Context, _, _, _ string, _ int) ([]onboarding.Knowledge, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]onboarding.Knowledge(nil), k.entries...), nil
}

func (k *memKnowledge) RecordAnswer(_ context.Context, _, _, ticket, author string, q report.Question) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.recorded = append(k.recorded, ticket+" "+author+": "+q.Answer)
	return nil
}

type questionEnv struct {
	flowEnv
	qs        *app.Questions
	llm       *fakeLLM
	knowledge *memKnowledge
}

// newQuestions returns a flow environment whose implement stage asks a
// blocking question in its first session.
func newQuestions(t *testing.T, script func(string, int) report.Outcome) questionEnv {
	t.Helper()
	e := newFlows(t, script)
	e.forge.checks = forge.ChecksSuccess
	llm := &fakeLLM{}
	k := &memKnowledge{entries: []onboarding.Knowledge{{ID: "k1", Kind: "decision", Title: "Identity provider",
		Body: "We use Keycloak."}}}
	qs := &app.Questions{Store: e.st, Items: e.st, Tenancy: e.st, Authz: e.flows.Authz, Orchestrator: e.flows.Orchestrator,
		Knowledge: k, LLM: llm, Model: "m", Now: time.Now, NewID: store.NewID}
	qs.Register(e.flows.Orchestrator)
	at := &app.AgentTracker{Reports: e.st, RunStore: e.st, Items: e.st, Tenancy: e.st, Authz: e.flows.Authz,
		Now: time.Now, NewID: store.NewID, OnQuestion: qs.Route}
	e.runner.mu.Lock()
	e.runner.ask = func(r run.Run, n int) {
		if r.Stage != "implement" || n != 1 {
			return
		}
		it, err := e.st.ItemByID(context.Background(), r.TicketID)
		if err != nil {
			return
		}
		_, _ = at.RaiseQuestion(context.Background(), app.RunCaller{RunID: r.ID, Customer: "acme", Project: "WEB", Ticket: it.Key},
			"Which identity provider do we use?", "Keycloak or Okta.", true)
	}
	e.runner.mu.Unlock()
	return questionEnv{flowEnv: e, qs: qs, llm: llm, knowledge: k}
}

func toolUse(name, input string) *app.LLMResponse {
	return &app.LLMResponse{StopReason: "tool_use", Content: []planner.Block{
		{Type: planner.BlockToolUse, ToolUseID: "t-" + name, Name: name, Input: json.RawMessage(input)}}}
}

func (e questionEnv) question(t *testing.T, ticketID string) report.Question {
	t.Helper()
	qs, err := e.st.Questions(t.Context(), ticketID)
	require.NoError(t, err)
	require.Len(t, qs, 1)
	return qs[0]
}

func TestQuestions_PlannerAnswersFromSourcesAndTheStageResumes(t *testing.T) {
	e := newQuestions(t, func(stage string, n int) report.Outcome {
		if stage == "implement" && n == 1 {
			return report.OutcomeBlocked
		}
		return report.OutcomeDone
	})
	e.llm.script(
		toolUse("search_knowledge", `{"query":"identity provider"}`),
		toolUse("answer", `{"answer":"Keycloak.","sources":["k1"]}`),
	)
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)

	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	q := e.question(t, tk.ID)
	assert.Equal(t, report.QuestionAnswered, q.Status)
	assert.Equal(t, "planner", q.AnsweredBy)
	assert.Equal(t, "Keycloak.\n\nSources: k1", q.Answer)
	assert.Equal(t, []string{"implement", "implement", "review", "verify"}, e.runner.ran(), "a new implement session")
	e.runner.mu.Lock()
	assert.Contains(t, e.runner.prompts["implement#2"], "**Question:** Which identity provider do we use?")
	assert.Contains(t, e.runner.prompts["implement#2"], "Keycloak.")
	e.runner.mu.Unlock()
	assert.Equal(t, []string{tk.Key + " planner: Keycloak.\n\nSources: k1"}, e.knowledge.recorded)
	history := e.events(t, tk.Key)
	assert.Contains(t, history, "item.question_answered")
	assert.Contains(t, history, "flow.resumed")

	// The planner saw the ticket, the question and the knowledge it found.
	first := e.llm.request(0)
	assert.Equal(t, app.QuestionInstructions, first.System)
	assert.Equal(t, tk.Key, first.Caller.TicketKey)
	assert.Contains(t, first.Messages[0].Content[0].Text, "Which identity provider do we use?")
	assert.Contains(t, e.llm.request(1).Messages[2].Content[0].Text, "We use Keycloak.")
}

func TestQuestions_EscalatedQuestionsWaitForAHuman(t *testing.T) {
	e := newQuestions(t, func(stage string, n int) report.Outcome {
		if stage == "implement" && n == 1 {
			return report.OutcomeBlocked
		}
		return report.OutcomeDone
	})
	e.llm.script(toolUse("answer", `{"answer":"Okta, probably.","sources":[]}`), // no sources: refused
		toolUse("escalate", `{"reason":"No source names the provider."}`))
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)

	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "question" })
	require.Eventually(t, func() bool {
		qs, _ := e.st.Questions(t.Context(), tk.ID)
		return len(qs) == 1 && qs[0].Route == report.RouteHuman
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, tracker.StateWaitingForAnswer, e.state(t, tk.Key))
	assert.Contains(t, e.events(t, tk.Key), "item.question_escalated")
	q := e.question(t, tk.ID)

	_, err = e.qs.Answer(user(t, "carol", "acme-viewers"), q.ID, "Keycloak")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = e.qs.Answer(user(t, "bob", "acme-devs"), q.ID, " ")
	assert.ErrorIs(t, err, app.ErrInvalid)
	v, err := e.qs.Answer(user(t, "bob", "acme-devs"), q.ID, "Keycloak, see the platform docs.")
	require.NoError(t, err)
	assert.Equal(t, tk.Key, v.TicketKey)
	_, err = e.qs.Answer(user(t, "bob", "acme-devs"), q.ID, "again")
	assert.ErrorIs(t, err, app.ErrConflict)

	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	e.runner.mu.Lock()
	assert.Contains(t, e.runner.prompts["implement#2"], "Keycloak, see the platform docs.")
	e.runner.mu.Unlock()
	assert.Equal(t, []string{tk.Key + " bob: Keycloak, see the platform docs."}, e.knowledge.recorded)
}

func TestQuestions_NonBlockingQuestionsDoNotStopTheStage(t *testing.T) {
	e := newQuestions(t, always(report.OutcomeDone))
	e.runner.mu.Lock()
	ask := e.runner.ask
	e.runner.ask = nil
	e.runner.mu.Unlock()
	_ = ask
	e.llm.script(toolUse("escalate", `{"reason":"unknown"}`))
	tk := e.ticket(t, auto)
	at := &app.AgentTracker{Reports: e.st, RunStore: e.st, Items: e.st, Tenancy: e.st, Authz: e.flows.Authz,
		Now: time.Now, NewID: store.NewID, OnQuestion: e.qs.Route}
	e.runner.mu.Lock()
	e.runner.ask = func(r run.Run, n int) {
		if r.Stage == "implement" {
			_, _ = at.RaiseQuestion(context.Background(), app.RunCaller{RunID: r.ID, Customer: "acme", Project: "WEB",
				Ticket: tk.Key}, "Should the button be blue?", "", false)
		}
	}
	e.runner.mu.Unlock()
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	assert.Equal(t, []string{"implement", "review", "verify"}, e.runner.ran())
}

func TestQuestions_IterationLimitAnswerResumesTheLoop(t *testing.T) {
	e := newQuestions(t, func(stage string, n int) report.Outcome {
		if stage == "review" && n <= 2 {
			return report.OutcomeFailed
		}
		return report.OutcomeDone
	})
	e.runner.mu.Lock()
	e.runner.ask = nil
	e.runner.mu.Unlock()
	dave := user(t, "dave", "acme-admins")
	def := pipeline.Default()
	def.MaxIterations = 1
	_, err := e.flows.Pipelines.Save(dave, "WEB", "default", def, 0)
	require.NoError(t, err)
	tk := e.ticket(t, auto)
	_, err = e.flows.Start(dave, tk.Key)
	require.NoError(t, err)

	f := e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Waiting == "question" })
	assert.Equal(t, "implement", f.Stage, "the flow continues where the loop went")
	q := e.question(t, tk.ID)
	assert.Equal(t, report.RouteHuman, q.Route, "questions about the process go to humans")
	_, err = e.qs.Answer(user(t, "bob", "acme-devs"), q.ID, "Use the simpler design from the review.")
	require.NoError(t, err)

	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })
	assert.Equal(t, []string{"implement", "review", "implement", "review", "implement", "review", "verify"}, e.runner.ran())
	e.runner.mu.Lock()
	assert.True(t, strings.Contains(e.runner.prompts["implement#3"], "Use the simpler design from the review."))
	e.runner.mu.Unlock()
}
