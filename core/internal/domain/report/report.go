// Package report models what agent runs report about their ticket:
// progress notes, stage reports, assumptions they made, and questions they
// could not answer themselves.
package report

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind of a report.
type Kind string

// Report kinds.
const (
	KindProgress    Kind = "progress"     // what the agent is doing
	KindStageReport Kind = "stage_report" // the agent's account of its stage
	KindAssumption  Kind = "assumption"   // a reversible decision taken without asking
)

// Outcome of a stage report.
type Outcome string

// Outcomes.
const (
	OutcomeDone    Outcome = "done"    // the stage's work is complete
	OutcomeBlocked Outcome = "blocked" // cannot continue without help
	OutcomeFailed  Outcome = "failed"  // tried and failed
)

// Report is one report of a run.
type Report struct {
	ID        string
	ProjectID string
	TicketID  string
	RunID     string
	Kind      Kind
	Outcome   Outcome // stage reports
	Text      string  // Markdown
	Detail    string  // stage reports: details; assumptions: rationale
	CreatedAt time.Time
}

// MaxText bounds report texts.
const MaxText = 20_000

// Validate checks a report.
func (r Report) Validate() error {
	var errs []error
	switch r.Kind {
	case KindProgress, KindAssumption:
	case KindStageReport:
		if r.Outcome != OutcomeDone && r.Outcome != OutcomeBlocked && r.Outcome != OutcomeFailed {
			errs = append(errs, fmt.Errorf("outcome %q must be done, blocked or failed", r.Outcome))
		}
	default:
		errs = append(errs, fmt.Errorf("unknown report kind %q", r.Kind))
	}
	if strings.TrimSpace(r.Text) == "" || utf8.RuneCountInString(r.Text) > MaxText {
		errs = append(errs, fmt.Errorf("text must be 1-%d characters", MaxText))
	}
	if utf8.RuneCountInString(r.Detail) > MaxText {
		errs = append(errs, fmt.Errorf("detail must be at most %d characters", MaxText))
	}
	return errors.Join(errs...)
}

// QuestionStatus is where a question stands.
type QuestionStatus string

// Question statuses.
const (
	QuestionOpen     QuestionStatus = "open"
	QuestionAnswered QuestionStatus = "answered"
)

// Routes of an open question (ADR-0015).
const (
	RoutePlanner = "planner" // the planner tries to answer from sources
	RouteHuman   = "human"   // in the humans' inbox
)

// Question is something a run could not decide on its own.
type Question struct {
	ID         string
	ProjectID  string
	TicketID   string
	RunID      string
	Text       string // the question
	Context    string // what the agent knows, options considered
	Blocking   bool   // the ticket cannot proceed without an answer
	Status     QuestionStatus
	Route      string // RoutePlanner, RouteHuman or "" (not routed yet)
	Answer     string
	AnsweredBy string
	CreatedAt  time.Time
	AnsweredAt time.Time
}

// Validate checks a new question.
func (q Question) Validate() error {
	if strings.TrimSpace(q.Text) == "" || utf8.RuneCountInString(q.Text) > 5000 {
		return errors.New("question must be 1-5000 characters")
	}
	if utf8.RuneCountInString(q.Context) > MaxText {
		return fmt.Errorf("context must be at most %d characters", MaxText)
	}
	return nil
}
