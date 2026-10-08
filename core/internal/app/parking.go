package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/agent"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
)

// Questions answered online (ADR-0026): a session that raised a blocking
// question is held open; an answer that comes within the project's answer
// window goes into the same session; past the window the session parks
// (work pushed, state saved) and a later session of its stage resumes it
// with the answers.

// DefaultAnswerWindow is how long sessions wait for answers when the
// project sets no window.
const DefaultAnswerWindow = 15 * time.Minute

// QuestionLister lists a ticket's questions.
type QuestionLister interface {
	Questions(ctx context.Context, ticketID string) ([]report.Question, error)
}

// OpenQuestionLister lists all open questions.
type OpenQuestionLister interface {
	OpenQuestions(ctx context.Context) ([]report.Question, error)
}

// Hold keeps a run's session open after its turn ends, waiting for the
// answer to a blocking question it raised. It reports whether the session
// waits; otherwise the session ends as before (ADR-0015).
func (rs *Runs) Hold(ctx context.Context, runID string) bool {
	r, err := rs.Store.Run(ctx, runID)
	if err != nil || r.Status != run.StatusRunning || r.Spec.Session == nil || rs.Dispatcher == nil {
		return false
	}
	if err := rs.Dispatcher.input(ctx, r, "hold", ""); err != nil {
		rs.logger().WarnContext(ctx, "could not hold the session for an answer", "run", r.ID, "error", err)
		return false
	}
	return true
}

// DeliverAnswer gives an answered question to the session that asked it,
// when that session still waits; once none of its blocking questions is
// open, the session is released and ends with its next turn.
func (rs *Runs) DeliverAnswer(ctx context.Context, q report.Question) {
	if q.RunID == "" || rs.Dispatcher == nil {
		return
	}
	r, err := rs.Store.Run(ctx, q.RunID)
	if err != nil || r.Status != run.StatusRunning || r.Spec.Session == nil {
		return // parked or ended: the stage resumes with the answers
	}
	text := fmt.Sprintf("Answer to your question (by %s):\n\n> %s\n\n%s", q.AnsweredBy,
		strings.ReplaceAll(strings.TrimSpace(q.Text), "\n", "\n> "), q.Answer)
	if err := rs.Dispatcher.input(ctx, r, "message", text); err != nil {
		rs.logger().WarnContext(ctx, "could not deliver an answer to its session", "run", r.ID, "error", err)
		return
	}
	if rs.Questions == nil {
		return
	}
	qs, err := rs.Questions.Questions(ctx, r.TicketID)
	if err != nil {
		return
	}
	for _, x := range qs {
		if x.RunID == r.ID && x.Blocking && x.Status == report.QuestionOpen {
			return // still waiting for another answer
		}
	}
	if err := rs.Dispatcher.input(ctx, r, "release", ""); err != nil {
		rs.logger().WarnContext(ctx, "could not release the session", "run", r.ID, "error", err)
	}
}

// ParkWaiting parks the sessions that have waited for an answer longer
// than their project's answer window.
func (rs *Runs) ParkWaiting(ctx context.Context, open OpenQuestionLister) {
	qs, err := open.OpenQuestions(ctx)
	if err != nil {
		rs.logger().ErrorContext(ctx, "list open questions failed", "error", err)
		return
	}
	now := rs.Now()
	seen := map[string]bool{}
	for _, q := range qs {
		if !q.Blocking || q.RunID == "" || seen[q.RunID] {
			continue
		}
		seen[q.RunID] = true
		r, err := rs.Store.Run(ctx, q.RunID)
		if err != nil || r.Status != run.StatusRunning || r.Spec.Session == nil {
			continue
		}
		if now.Sub(q.CreatedAt) < rs.answerWindow(ctx, r.ProjectID) {
			continue
		}
		if err := rs.Dispatcher.input(ctx, r, "park", ""); err != nil {
			rs.logger().WarnContext(ctx, "could not park a waiting session", "run", r.ID, "error", err)
			continue
		}
		rs.logger().InfoContext(ctx, "parked a session waiting for answers", "run", r.ID, "question", q.ID)
	}
}

// ParkLoop runs ParkWaiting every interval until ctx ends.
func (rs *Runs) ParkLoop(ctx context.Context, open OpenQuestionLister, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rs.ParkWaiting(ctx, open)
		}
	}
}

func (rs *Runs) answerWindow(ctx context.Context, projectID string) time.Duration {
	w := rs.AnswerWindow
	if w <= 0 {
		w = DefaultAnswerWindow
	}
	if rs.Execution == nil {
		return w
	}
	if x, err := rs.Execution.ExecutionSettings(ctx, projectID); err == nil && x.AnswerWindowMinutes > 0 {
		w = time.Duration(x.AnswerWindowMinutes) * time.Minute
	}
	return w
}

// resumable returns the parked session a new session of a ticket's stage
// continues, with the answers to give it: the stage's latest run, when it
// parked with its state saved.
func (rs *Runs) resumable(ctx context.Context, ticketID, stage string) (*agent.Resume, string, bool) {
	if rs.Questions == nil {
		return nil, "", false
	}
	runs, err := rs.Store.ListRuns(ctx, RunFilter{TicketID: ticketID})
	if err != nil {
		return nil, "", false
	}
	var prev *run.Run
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Stage == stage {
			prev = &runs[i]
			break
		}
	}
	if prev == nil || prev.Result == nil || !prev.Result.Parked || prev.Result.SessionID == "" {
		return nil, "", false
	}
	if _, err := rs.Store.RunState(ctx, prev.ID); err != nil {
		if !errors.Is(err, ErrNotFound) {
			rs.logger().WarnContext(ctx, "read parked session state failed", "run", prev.ID, "error", err)
		}
		return nil, "", false
	}
	qs, err := rs.Questions.Questions(ctx, ticketID)
	if err != nil {
		return nil, "", false
	}
	var b strings.Builder
	b.WriteString("Your questions were answered while your session was parked:")
	for _, q := range qs {
		if q.RunID == prev.ID && q.Status == report.QuestionAnswered {
			fmt.Fprintf(&b, "\n\n**Question:** %s\n\n**Answer** (%s): %s", q.Text, q.AnsweredBy, q.Answer)
		}
	}
	b.WriteString("\n\nThe repository has your work in progress (a WIP commit). Continue the stage where you left off.")
	return &agent.Resume{RunID: prev.ID, SessionID: prev.Result.SessionID}, b.String(), true
}
