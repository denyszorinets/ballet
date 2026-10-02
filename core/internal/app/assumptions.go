package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// AssumptionStore persists the assumption register.
type AssumptionStore interface {
	Report(ctx context.Context, id string) (report.Report, error)
	// Assumptions returns a project's assumptions, newest first; review is
	// "", "open", "confirmed" or "rejected".
	Assumptions(ctx context.Context, projectID, review string) ([]report.Report, error)
	// ReviewAssumption records the review of an unreviewed assumption
	// (ErrConflict when reviewed meanwhile).
	ReviewAssumption(ctx context.Context, r report.Report, e event.Event) error
}

// Assumptions is the assumption register: humans review the reversible
// decisions agents took without asking. A rejection creates follow-up
// work: while the ticket is unresolved, an answered question its next
// sessions see; once it is resolved, a changeset proposing a correction
// ticket.
type Assumptions struct {
	Store      AssumptionStore
	Questions  QuestionStore
	Reports    ReportStore
	Items      ItemStore
	Tenancy    TenancyStore
	Authz      Authorizer
	Changesets *Changesets
	Now        func() time.Time
	NewID      func() string
}

// AssumptionView is an assumption with its ticket.
type AssumptionView struct {
	report.Report
	TicketKey   string
	TicketTitle string
}

// List returns a project's assumptions (tracker.read); review is "",
// "open", "confirmed" or "rejected".
func (as *Assumptions) List(ctx context.Context, projectKey, review string) ([]AssumptionView, error) {
	switch review {
	case "", "open", string(report.ReviewConfirmed), string(report.ReviewRejected):
	default:
		return nil, fmt.Errorf("%w: review must be open, confirmed or rejected", ErrInvalid)
	}
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	p, err := as.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return nil, err
	}
	c, err := as.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return nil, err
	}
	if err := as.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return nil, err
	}
	list, err := as.Store.Assumptions(ctx, p.ID, review)
	if err != nil {
		return nil, err
	}
	tickets := map[string]tracker.Item{}
	out := make([]AssumptionView, 0, len(list))
	for _, r := range list {
		it, ok := tickets[r.TicketID]
		if !ok {
			if it, err = as.Items.ItemByID(ctx, r.TicketID); err != nil {
				return nil, err
			}
			tickets[r.TicketID] = it
		}
		out = append(out, AssumptionView{Report: r, TicketKey: it.Key, TicketTitle: it.Title})
	}
	return out, nil
}

// Review confirms or rejects an assumption as the human caller
// (tracker.write). A rejection needs a comment and creates follow-up work.
func (as *Assumptions) Review(ctx context.Context, reportID string, confirm bool, comment string) (AssumptionView, error) {
	id, err := caller(ctx)
	if err != nil {
		return AssumptionView{}, err
	}
	r, err := as.Store.Report(ctx, reportID)
	if err != nil {
		return AssumptionView{}, err
	}
	if r.Kind != report.KindAssumption {
		return AssumptionView{}, fmt.Errorf("%w: report %s is not an assumption", ErrNotFound, reportID)
	}
	it, err := as.Items.ItemByID(ctx, r.TicketID)
	if err != nil {
		return AssumptionView{}, err
	}
	p, err := as.Tenancy.ProjectByID(ctx, r.ProjectID)
	if err != nil {
		return AssumptionView{}, err
	}
	c, err := as.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return AssumptionView{}, err
	}
	if err := as.Authz.Authorize(ctx, id, ActTrackerWrite, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return AssumptionView{}, err
	}
	comment = strings.TrimSpace(comment)
	if utf8.RuneCountInString(comment) > report.MaxText || (!confirm && comment == "") {
		return AssumptionView{}, fmt.Errorf("%w: a rejection needs a comment of at most %d characters", ErrInvalid, report.MaxText)
	}
	if r.Review != "" {
		return AssumptionView{}, fmt.Errorf("%w: the assumption is %s already", ErrConflict, r.Review)
	}
	now := as.Now()
	r.Review, r.ReviewComment, r.ReviewedBy, r.ReviewedAt = report.ReviewConfirmed, comment, id.Subject, now
	if !confirm {
		r.Review = report.ReviewRejected
		if r.FollowUp, err = as.followUp(ctx, r, it, c.ID, p.Key, id.Subject); err != nil {
			return AssumptionView{}, err
		}
	}
	e := event.Event{Customer: c.ID, Project: p.ID, EntityType: "item", EntityID: it.ID, Type: "item.assumption_reviewed",
		Actor: actorIn(ctx, id), OccurredAt: now,
		Payload: mustJSON(map[string]any{"report": r.ID, "review": r.Review, "follow_up": r.FollowUp})}
	if err := as.Store.ReviewAssumption(ctx, r, e); err != nil {
		return AssumptionView{}, err
	}
	return AssumptionView{Report: r, TicketKey: it.Key, TicketTitle: it.Title}, nil
}

// followUp creates the work a rejection causes and returns its reference:
// "question:<id>" or "changeset:<id>".
func (as *Assumptions) followUp(ctx context.Context, r report.Report, it tracker.Item, customerID, projectKey, by string) (string, error) {
	if !tracker.Resolved(it.State) {
		q := report.Question{ID: as.NewID(), ProjectID: r.ProjectID, TicketID: r.TicketID, RunID: r.RunID,
			Text: "Assumption rejected: " + r.Text, Context: r.Detail, Status: report.QuestionOpen, Route: report.RouteHuman,
			CreatedAt: as.Now()}
		e := event.Event{Customer: customerID, Project: r.ProjectID, EntityType: "item", EntityID: it.ID, Type: "item.question_raised",
			Actor: event.Actor{Kind: event.ActorHuman, Subject: by}, OccurredAt: as.Now(),
			Payload: mustJSON(map[string]any{"question": q.ID, "blocking": false, "assumption": r.ID})}
		if err := as.Reports.CreateQuestion(ctx, q, e); err != nil {
			return "", err
		}
		q.Status, q.Answer, q.AnsweredBy, q.AnsweredAt = report.QuestionAnswered, r.ReviewComment, by, as.Now()
		ae := e
		ae.Type, ae.Payload = "item.question_answered", mustJSON(map[string]any{"question": q.ID, "by": by})
		if err := as.Questions.AnswerQuestion(ctx, q, nil, ae); err != nil {
			return "", err
		}
		return "question:" + q.ID, nil
	}
	title := "Correct assumption on " + it.Key + ": " + oneLine(r.Text, 120)
	v, err := as.Changesets.Propose(ctx, ProposeInput{ProjectKey: projectKey, Title: title,
		Summary: fmt.Sprintf("An assumption made while working on %s was rejected.", it.Key),
		Ops: []changeset.Op{
			{Kind: changeset.OpCreateItem, Ref: "fix", Create: &changeset.CreateItem{Kind: tracker.KindTicket,
				Title: oneLine("Correct: "+r.Text, 200), Type: tracker.TypeBug,
				Description: fmt.Sprintf("While working on %s an agent assumed:\n\n> %s\n\n%s\n\nA human rejected it:\n\n%s",
					it.Key, r.Text, r.Detail, r.ReviewComment)}},
			{Kind: changeset.OpAddDependency, Dependency: &changeset.AddDependency{From: it.Key, To: "$fix",
				Type: tracker.DepRelates}},
		}})
	if err != nil {
		return "", err
	}
	return "changeset:" + v.ID, nil
}
