package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// DigestStore reads what a digest summarizes.
type DigestStore interface {
	QueryEvents(ctx context.Context, q EventQuery) ([]event.Event, error)
	ActiveFlows(ctx context.Context) ([]Flow, error)
	OpenQuestions(ctx context.Context) ([]report.Question, error)
	Assumptions(ctx context.Context, projectID, review string) ([]report.Report, error)
	CountedTokens(ctx context.Context, f UsageFilter) (int64, error)
	ItemByID(ctx context.Context, id string) (tracker.Item, error)
}

// Digests summarize what happened in a project over a period — typically
// an unattended night — from its events and current state.
type Digests struct {
	Store   DigestStore
	Tenancy TenancyStore
	Authz   Authorizer
	Now     func() time.Time
}

// DigestTicket is a ticket in a digest.
type DigestTicket struct {
	Key, Title string
	At         time.Time
	Detail     string // e.g. the stage that failed, what it waits for
}

// DigestQuestion is an open question in a digest.
type DigestQuestion struct {
	Ticket   string
	Text     string
	Blocking bool
	Route    string
	Since    time.Time
}

// Digest summarizes a project over [Since, Until).
type Digest struct {
	ProjectKey   string
	Since, Until time.Time

	Done, Failed, Started, Merged []DigestTicket
	Waiting                       []DigestTicket // now: pipelines waiting, and for what
	QuestionsRaised               int
	AnsweredByPlanner             int
	AnsweredByHuman               int
	OpenQuestions                 []DigestQuestion // now
	Assumptions                   []DigestTicket   // recorded in the period; Detail: the assumption
	Proposals                     int              // changesets proposed (by agents and the planner)
	Runs                          map[string]int   // agent sessions finished, by status
	Tokens                        int64            // counted tokens used in the period
	Interventions                 []string         // pauses, kills, resumes
}

// Project returns the digest of a project for [since, until); a zero
// until means now. Needs tracker.read.
func (ds *Digests) Project(ctx context.Context, projectKey string, since, until time.Time) (Digest, error) {
	id, err := caller(ctx)
	if err != nil {
		return Digest{}, err
	}
	p, err := ds.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return Digest{}, err
	}
	c, err := ds.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return Digest{}, err
	}
	if err := ds.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return Digest{}, err
	}
	if until.IsZero() {
		until = ds.Now()
	}
	if !since.Before(until) {
		return Digest{}, fmt.Errorf("%w: since must be before until", ErrInvalid)
	}
	d := Digest{ProjectKey: p.Key, Since: since, Until: until, Runs: map[string]int{}}
	items := map[string]tracker.Item{}
	item := func(id string) tracker.Item {
		if it, ok := items[id]; ok {
			return it
		}
		it, err := ds.Store.ItemByID(ctx, id)
		if err != nil {
			it = tracker.Item{ID: id, Key: id}
		}
		items[id] = it
		return it
	}
	ticket := func(entityID string, at time.Time, detail string) DigestTicket {
		it := item(entityID)
		return DigestTicket{Key: it.Key, Title: it.Title, At: at, Detail: detail}
	}
	done := map[string]bool{}
	var after int64
	for {
		events, err := ds.Store.QueryEvents(ctx, EventQuery{Project: p.ID, AfterSeq: after, Since: since, Limit: 1000})
		if err != nil {
			return Digest{}, err
		}
		for _, e := range events {
			after = e.Seq
			if e.OccurredAt.Before(since) || !e.OccurredAt.Before(until) {
				continue
			}
			var pl map[string]any
			_ = json.Unmarshal(e.Payload, &pl)
			str := func(k string) string { s, _ := pl[k].(string); return s }
			switch e.Type {
			case "flow.started":
				d.Started = append(d.Started, ticket(e.EntityID, e.OccurredAt, str("pipeline")))
			case "flow.done":
				if !done[e.EntityID] {
					done[e.EntityID] = true
					d.Done = append(d.Done, ticket(e.EntityID, e.OccurredAt, ""))
				}
			case "item.state_changed":
				if str("to") == string(tracker.StateDone) && !done[e.EntityID] && item(e.EntityID).Kind == tracker.KindTicket {
					done[e.EntityID] = true
					d.Done = append(d.Done, ticket(e.EntityID, e.OccurredAt, "by hand"))
				}
			case "flow.failed":
				d.Failed = append(d.Failed, ticket(e.EntityID, e.OccurredAt, "at "+str("stage")))
			case "item.pr_updated":
				if str("state") == "merged" {
					d.Merged = append(d.Merged, ticket(e.EntityID, e.OccurredAt, ""))
				}
			case "item.question_raised":
				d.QuestionsRaised++
			case "item.question_answered":
				if str("by") == "planner" {
					d.AnsweredByPlanner++
				} else {
					d.AnsweredByHuman++
				}
			case "changeset.proposed":
				d.Proposals++
			case "run.finished":
				d.Runs[str("status")]++
			case "control.paused", "control.killed", "control.resumed":
				what := map[string]string{"control.paused": "paused", "control.killed": "stopped all runs",
					"control.resumed": "resumed"}[e.Type]
				line := fmt.Sprintf("%s %s %s", e.OccurredAt.UTC().Format("2006-01-02 15:04"), e.Actor.Subject, what)
				if r := str("reason"); r != "" {
					line += ": " + r
				}
				d.Interventions = append(d.Interventions, line)
			}
		}
		if len(events) < 1000 {
			break
		}
	}
	// Merged pull requests can be reported repeatedly by polling.
	d.Merged = uniqueTickets(d.Merged)

	flows, err := ds.Store.ActiveFlows(ctx)
	if err != nil {
		return Digest{}, err
	}
	for _, f := range flows {
		if f.ProjectID == p.ID && f.Status == FlowWaiting {
			d.Waiting = append(d.Waiting, ticket(f.TicketID, f.UpdatedAt, f.Waiting+" at "+f.Stage))
		}
	}
	open, err := ds.Store.OpenQuestions(ctx)
	if err != nil {
		return Digest{}, err
	}
	for _, q := range open {
		if q.ProjectID == p.ID {
			d.OpenQuestions = append(d.OpenQuestions, DigestQuestion{Ticket: item(q.TicketID).Key,
				Text: strings.SplitN(q.Text, "\n", 2)[0], Blocking: q.Blocking, Route: q.Route, Since: q.CreatedAt})
		}
	}
	sort.SliceStable(d.OpenQuestions, func(i, j int) bool { return d.OpenQuestions[i].Blocking && !d.OpenQuestions[j].Blocking })
	assumptions, err := ds.Store.Assumptions(ctx, p.ID, "")
	if err != nil {
		return Digest{}, err
	}
	for _, a := range assumptions {
		if !a.CreatedAt.Before(since) && a.CreatedAt.Before(until) {
			d.Assumptions = append(d.Assumptions, DigestTicket{Key: item(a.TicketID).Key, At: a.CreatedAt, Detail: a.Text})
		}
	}
	if d.Tokens, err = ds.Store.CountedTokens(ctx, UsageFilter{ProjectID: p.ID, Since: since, Until: until}); err != nil {
		return Digest{}, err
	}
	return d, nil
}

func uniqueTickets(ts []DigestTicket) []DigestTicket {
	seen := map[string]bool{}
	var out []DigestTicket
	for _, t := range ts {
		if !seen[t.Key] {
			seen[t.Key] = true
			out = append(out, t)
		}
	}
	return out
}

// Markdown renders the digest for reading or sharing.
func (d Digest) Markdown() string {
	var b strings.Builder
	f := func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }
	fmt.Fprintf(&b, "# Digest of %s\n\n%s — %s\n\n", d.ProjectKey, f(d.Since), f(d.Until))
	runs := 0
	for _, n := range d.Runs {
		runs += n
	}
	fmt.Fprintf(&b, "- **%d** tickets done, **%d** failed, **%d** started, **%d** pull requests merged\n",
		len(d.Done), len(d.Failed), len(d.Started), len(d.Merged))
	fmt.Fprintf(&b, "- **%d** agent sessions (%s), **%d** tokens\n", runs, statusList(d.Runs), d.Tokens)
	fmt.Fprintf(&b, "- **%d** questions raised: %d answered by the planner, %d by humans; **%d** open now\n",
		d.QuestionsRaised, d.AnsweredByPlanner, d.AnsweredByHuman, len(d.OpenQuestions))
	fmt.Fprintf(&b, "- **%d** assumptions recorded, **%d** changesets proposed\n", len(d.Assumptions), d.Proposals)
	section := func(title string, ts []DigestTicket) {
		if len(ts) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n## %s\n\n", title)
		for _, t := range ts {
			line := "- " + t.Key
			if t.Title != "" {
				line += " " + t.Title
			}
			if t.Detail != "" {
				line += " — " + t.Detail
			}
			b.WriteString(line + "\n")
		}
	}
	section("Done", d.Done)
	section("Merged", d.Merged)
	section("Failed", d.Failed)
	section("Waiting now", d.Waiting)
	if len(d.OpenQuestions) > 0 {
		b.WriteString("\n## Open questions\n\n")
		for _, q := range d.OpenQuestions {
			kind := "question"
			if q.Blocking {
				kind = "blocking"
			}
			fmt.Fprintf(&b, "- %s (%s, %s): %s\n", q.Ticket, kind, or(q.Route, "routing"), q.Text)
		}
	}
	section("Assumptions", d.Assumptions)
	if len(d.Interventions) > 0 {
		b.WriteString("\n## Interventions\n\n")
		for _, s := range d.Interventions {
			b.WriteString("- " + s + "\n")
		}
	}
	return b.String()
}

func statusList(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", m[k], k))
	}
	return strings.Join(parts, ", ")
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
