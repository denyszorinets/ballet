package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// ReportStore persists runs' reports and questions.
type ReportStore interface {
	CreateReport(ctx context.Context, r report.Report, e event.Event) error
	Reports(ctx context.Context, ticketID, runID string) ([]report.Report, error)
	CreateQuestion(ctx context.Context, q report.Question, e event.Event) error
	Questions(ctx context.Context, ticketID string) ([]report.Question, error)
}

// RunCaller is an agent run calling Core, as its run token says.
type RunCaller struct {
	RunID    string // from the token subject "run:<id>"
	Customer string
	Project  string
	Ticket   string
}

// AgentTracker is the tracker as agent runs see it (the tracker MCP
// server): their ticket's context, and reporting on it. A run reaches
// only its own ticket, and only while it is active.
type AgentTracker struct {
	Reports    ReportStore
	RunStore   RunStore
	Runs       *Runs // onboarding bundles
	Items      ItemStore
	Tenancy    TenancyStore
	Authz      Authorizer // humans listing reports
	Changesets *Changesets
	Now        func() time.Time
	NewID      func() string
}

type runScope struct {
	run      run.Run
	ticket   tracker.Item
	project  tenancy.Project
	customer tenancy.Customer
}

// scope checks that the caller is an active run of its ticket.
func (a *AgentTracker) scope(ctx context.Context, c RunCaller) (runScope, error) {
	r, err := a.RunStore.Run(ctx, c.RunID)
	if err != nil {
		return runScope{}, fmt.Errorf("%w: unknown run", ErrForbidden)
	}
	it, err := a.Items.ItemByID(ctx, r.TicketID)
	if err != nil {
		return runScope{}, err
	}
	p, err := a.Tenancy.ProjectByID(ctx, r.ProjectID)
	if err != nil {
		return runScope{}, err
	}
	cu, err := a.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return runScope{}, err
	}
	if it.Key != c.Ticket || p.Key != c.Project || cu.Key != c.Customer {
		return runScope{}, fmt.Errorf("%w: the token does not match the run", ErrForbidden)
	}
	if !r.Status.Active() {
		return runScope{}, fmt.Errorf("%w: run %s has ended", ErrForbidden, r.ID)
	}
	return runScope{run: r, ticket: it, project: p, customer: cu}, nil
}

func (s runScope) actor() event.Actor {
	return event.Actor{Kind: event.ActorService, Subject: "run:" + s.run.ID}
}

func (a *AgentTracker) itemEvent(s runScope, typ string, payload map[string]any) event.Event {
	return event.Event{Customer: s.customer.ID, Project: s.project.ID, EntityType: "item", EntityID: s.ticket.ID,
		Type: typ, Actor: s.actor(), OccurredAt: a.Now(), Payload: mustJSON(payload)}
}

// Context returns the run's ticket context (its onboarding bundle, current)
// with the reports and questions so far.
func (a *AgentTracker) Context(ctx context.Context, c RunCaller) (string, error) {
	s, err := a.scope(ctx, c)
	if err != nil {
		return "", err
	}
	b, err := a.Runs.Bundle(ctx, s.ticket, s.project, s.run.Stage, "")
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(b.Render(onboarding.DefaultMaxBytes))
	reports, err := a.Reports.Reports(ctx, s.ticket.ID, "")
	if err != nil {
		return "", err
	}
	if len(reports) > 0 {
		out.WriteString("\n## Reports so far\n\n")
		for _, r := range reports {
			fmt.Fprintf(&out, "- %s (%s%s): %s\n", r.CreatedAt.Format(time.RFC3339), r.Kind, outcome(r), oneLine(r.Text, 300))
		}
	}
	qs, err := a.Reports.Questions(ctx, s.ticket.ID)
	if err != nil {
		return "", err
	}
	for i, q := range qs {
		if i == 0 {
			out.WriteString("\n## Questions\n\n")
		}
		answer := "open"
		if q.Status == report.QuestionAnswered {
			answer = "answered: " + q.Answer
		}
		fmt.Fprintf(&out, "- %s — %s\n", oneLine(q.Text, 300), oneLine(answer, 1000))
	}
	return out.String(), nil
}

func outcome(r report.Report) string {
	if r.Outcome == "" {
		return ""
	}
	return ", " + string(r.Outcome)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Report records a progress note, a stage report or an assumption.
func (a *AgentTracker) Report(ctx context.Context, c RunCaller, kind report.Kind, outcome report.Outcome, text, detail string) (report.Report, error) {
	s, err := a.scope(ctx, c)
	if err != nil {
		return report.Report{}, err
	}
	r := report.Report{ID: a.NewID(), ProjectID: s.project.ID, TicketID: s.ticket.ID, RunID: s.run.ID, Kind: kind,
		Outcome: outcome, Text: text, Detail: detail, CreatedAt: a.Now()}
	if err := r.Validate(); err != nil {
		return report.Report{}, invalid(err)
	}
	e := a.itemEvent(s, "item.report_added", map[string]any{"run": s.run.ID, "kind": kind, "outcome": outcome})
	if err := a.Reports.CreateReport(ctx, r, e); err != nil {
		return report.Report{}, err
	}
	return r, nil
}

// RaiseQuestion records a question the run cannot answer itself. Routing
// to the planner and the human comes with the questions feature.
func (a *AgentTracker) RaiseQuestion(ctx context.Context, c RunCaller, text, background string, blocking bool) (report.Question, error) {
	s, err := a.scope(ctx, c)
	if err != nil {
		return report.Question{}, err
	}
	q := report.Question{ID: a.NewID(), ProjectID: s.project.ID, TicketID: s.ticket.ID, RunID: s.run.ID, Text: text,
		Context: background, Blocking: blocking, Status: report.QuestionOpen, CreatedAt: a.Now()}
	if err := q.Validate(); err != nil {
		return report.Question{}, invalid(err)
	}
	e := a.itemEvent(s, "item.question_raised", map[string]any{"run": s.run.ID, "question": q.ID, "blocking": blocking})
	if err := a.Reports.CreateQuestion(ctx, q, e); err != nil {
		return report.Question{}, err
	}
	return q, nil
}

// ProposeWork turns work a run discovered into a plan changeset for the
// planner and the human to decide on; runs never create tickets.
func (a *AgentTracker) ProposeWork(ctx context.Context, c RunCaller, title, description string, typ tracker.TicketType, reason string) (ChangesetView, error) {
	s, err := a.scope(ctx, c)
	if err != nil {
		return ChangesetView{}, err
	}
	if typ == "" {
		typ = tracker.TypeFeature
	}
	summary := fmt.Sprintf("Proposed by run %s (%s stage of %s).\n\n%s", s.run.ID, s.run.Stage, s.ticket.Key, strings.TrimSpace(reason))
	return a.Changesets.proposeAs(ctx, s.project, s.customer, ProposeInput{
		ProjectKey: s.project.Key, Title: "Discovered: " + title, Summary: summary,
		Ops: []changeset.Op{
			{Kind: changeset.OpCreateItem, Ref: "work", Create: &changeset.CreateItem{Kind: tracker.KindTicket, Title: title,
				Description: description, Type: typ}},
			{Kind: changeset.OpAddDependency, Dependency: &changeset.AddDependency{From: s.ticket.Key, To: "$work", Type: tracker.DepRelates}},
		},
	}, s.actor())
}

// TicketReports returns a ticket's reports and questions for a human
// (tracker.read).
func (a *AgentTracker) TicketReports(ctx context.Context, ticketKey string) ([]report.Report, []report.Question, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, nil, err
	}
	it, err := a.Items.ItemByKey(ctx, ticketKey)
	if err != nil {
		return nil, nil, err
	}
	p, err := a.Tenancy.ProjectByID(ctx, it.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	cu, err := a.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return nil, nil, err
	}
	if err := a.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Customer: cu.Key, Project: p.Key}); err != nil {
		return nil, nil, err
	}
	rs, err := a.Reports.Reports(ctx, it.ID, "")
	if err != nil {
		return nil, nil, err
	}
	qs, err := a.Reports.Questions(ctx, it.ID)
	return rs, qs, err
}
