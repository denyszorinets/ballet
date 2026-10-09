package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// parkingEnv has a running agent session on a ticket.
type parkingEnv struct {
	runsEnv
	fr  *fakeAgent
	tk  app.ItemView
	run app.RunView
}

func newParking(t *testing.T) parkingEnv {
	t.Helper()
	e := newRuns(t, time.Minute)
	e.runs.Questions = e.st
	dave := user(t, "dave", "acme-admins")
	tk := mk(t, e.tr, tracker.KindTicket, "Login")
	fr := &fakeAgent{}
	require.NoError(t, e.d.Connect(t.Context(), app.AgentInfo{Name: "r1", Capacity: 3}, fr))
	v, err := e.runs.CreateAgent(dave, tk.Key, "implement", app.AgentInput{Adapter: "claude-code", Prompt: "p"})
	require.NoError(t, err)
	e.eventually(t, v.ID, run.StatusStarting)
	require.NoError(t, e.d.Running(t.Context(), "r1", v.ID))
	return parkingEnv{runsEnv: e, fr: fr, tk: tk, run: v}
}

// ask records a blocking question of the run, asked at.
func (e parkingEnv) ask(t *testing.T, text string, at time.Time) report.Question {
	t.Helper()
	p, err := e.st.ProjectByID(t.Context(), e.run.ProjectID)
	require.NoError(t, err)
	q := report.Question{ID: text, ProjectID: p.ID, TicketID: e.run.TicketID, RunID: e.run.ID, Text: text,
		Blocking: true, Status: report.QuestionOpen, CreatedAt: at}
	require.NoError(t, e.st.CreateQuestion(t.Context(), q, event.Event{Organization: p.OrganizationID, Project: p.ID,
		EntityType: "item", EntityID: e.run.TicketID, Type: "item.question_raised", Actor: event.System, OccurredAt: at,
		Payload: []byte(`{}`)}))
	return q
}

func (e parkingEnv) answer(t *testing.T, q report.Question, text string) report.Question {
	t.Helper()
	p, err := e.st.ProjectByID(t.Context(), q.ProjectID)
	require.NoError(t, err)
	q.Status, q.Answer, q.AnsweredBy, q.AnsweredAt = report.QuestionAnswered, text, "alice", time.Now()
	require.NoError(t, e.st.AnswerQuestion(t.Context(), q, nil, event.Event{Organization: p.OrganizationID, Project: p.ID,
		EntityType: "item", EntityID: q.TicketID, Type: "item.question_answered", Actor: event.System,
		OccurredAt: time.Now(), Payload: []byte(`{}`)}))
	return q
}

func (e parkingEnv) inputs() []string {
	e.fr.mu.Lock()
	defer e.fr.mu.Unlock()
	return append([]string(nil), e.fr.inputs...)
}

func TestParking_AnswersReachTheWaitingSession(t *testing.T) {
	e := newParking(t)
	require.True(t, e.runs.Hold(t.Context(), e.run.ID))
	q1 := e.ask(t, "Which IdP?", time.Now())
	q2 := e.ask(t, "Which port?", time.Now())

	e.runs.DeliverAnswer(t.Context(), e.answer(t, q1, "Keycloak."))
	e.runs.DeliverAnswer(t.Context(), e.answer(t, q2, "8080."))
	got := e.inputs()
	require.Len(t, got, 4)
	assert.Equal(t, e.run.ID+" hold: ", got[0])
	assert.Contains(t, got[1], e.run.ID+" message: Answer to your question (by alice):\n\n> Which IdP?\n\nKeycloak.")
	assert.Contains(t, got[2], "Which port?", "still waiting for the second answer: no release yet")
	assert.Equal(t, e.run.ID+" release: ", got[3], "released once nothing it asked is open")
}

func TestParking_SessionsParkAfterTheAnswerWindow(t *testing.T) {
	e := newParking(t)
	e.runs.AnswerWindow = 10 * time.Minute
	q := e.ask(t, "Fresh?", time.Now())
	e.runs.ParkWaiting(t.Context(), e.st)
	assert.Empty(t, e.inputs(), "within the window")

	e.runs.Now = func() time.Time { return q.CreatedAt.Add(11 * time.Minute) }
	e.runs.ParkWaiting(t.Context(), e.st)
	assert.Equal(t, []string{e.run.ID + " park: "}, e.inputs())
}

func TestParking_AParkedSessionResumesWithItsAnswers(t *testing.T) {
	e := newParking(t)
	q := e.ask(t, "Which IdP?", time.Now())
	require.NoError(t, e.d.Finished(t.Context(), "r1", e.run.ID, 0, "", false, &app.SessionResult{Success: true,
		Summary: "Waiting.", Parked: true, SessionID: "s-1", State: []byte("transcript")}))
	parked := e.status(t, e.run.ID)
	assert.Equal(t, run.StatusSucceeded, parked.Status)
	assert.True(t, parked.Result.Parked)
	assert.Equal(t, "s-1", parked.Result.SessionID)

	e.answer(t, q, "Keycloak.")
	next, err := e.runs.CreateAgent(user(t, "dave", "acme-admins"), e.tk.Key, "implement",
		app.AgentInput{Adapter: "claude-code", Prompt: "p"})
	require.NoError(t, err)
	sess := next.Spec.Session
	require.NotNil(t, sess.Resume)
	assert.Equal(t, "s-1", sess.Resume.SessionID)
	assert.Contains(t, sess.Prompt, "**Question:** Which IdP?\n\n**Answer** (alice): Keycloak.")
	assert.NotContains(t, sess.Prompt, "# "+e.tk.Key, "the resumed session knows its ticket already")

	e.eventually(t, next.ID, run.StatusStarting)
	e.fr.mu.Lock()
	started := e.fr.runs[len(e.fr.runs)-1]
	e.fr.mu.Unlock()
	assert.Equal(t, []byte("transcript"), started.Spec.Session.Resume.State, "the state goes to the agent at start")

	stored := e.status(t, next.ID)
	assert.Nil(t, stored.Spec.Session.Resume.State, "and is not stored with the run")

	// Another stage starts afresh.
	other, err := e.runs.CreateAgent(user(t, "dave", "acme-admins"), e.tk.Key, "review",
		app.AgentInput{Adapter: "claude-code", Prompt: "p"})
	require.NoError(t, err)
	assert.Nil(t, other.Spec.Session.Resume)
}

func TestParking_WithoutStateTheStageStartsAfresh(t *testing.T) {
	e := newParking(t)
	require.NoError(t, e.d.Finished(context.Background(), "r1", e.run.ID, 0, "", false,
		&app.SessionResult{Success: true, Parked: true, SessionID: "s-1"}))
	assert.Empty(t, e.status(t, e.run.ID).Result.SessionID, "nothing to resume")
	next, err := e.runs.CreateAgent(user(t, "dave", "acme-admins"), e.tk.Key, "implement",
		app.AgentInput{Adapter: "claude-code", Prompt: "p"})
	require.NoError(t, err)
	assert.Nil(t, next.Spec.Session.Resume)
}
