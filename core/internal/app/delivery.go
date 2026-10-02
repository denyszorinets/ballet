package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
)

// DeliveryObserver records delivery measurements (Prometheus in
// production). Customer and project are keys.
type DeliveryObserver interface {
	StageFinished(customer, project, stage, outcome string)
	FlowFinished(customer, project, status string, leadTime time.Duration, iterations int)
	FlowWaiting(customer, project, reason string)
	RunFinished(customer, project, stage, status string, duration time.Duration)
	QuestionRaised(customer, project string, blocking bool)
	QuestionAnswered(customer, project, by string, wait time.Duration)
}

// Delivery turns Core's event log into delivery measurements: it follows
// the log from the moment it starts, so every flow, run and question event
// is counted however it came about.
type Delivery struct {
	Log      EventLog
	Flows    FlowStore
	Runs     RunStore
	Reports  QuestionStore
	Tenancy  TenancyStore
	Observer DeliveryObserver
	Interval time.Duration // default 2 s
	Logger   *slog.Logger

	mu   sync.Mutex
	keys map[string]string // customer/project ID → key
}

// Run observes events until ctx is cancelled.
func (d *Delivery) Run(ctx context.Context) {
	interval := d.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	cursor, err := d.Log.LastEventSeq(ctx)
	if err != nil && d.Logger != nil {
		d.Logger.WarnContext(ctx, "delivery metrics: reading the event log failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		cursor = d.Observe(ctx, cursor)
	}
}

// Observe processes events after cursor and returns the new cursor.
func (d *Delivery) Observe(ctx context.Context, cursor int64) int64 {
	for {
		events, err := d.Log.QueryEvents(ctx, EventQuery{AfterSeq: cursor, Limit: 500})
		if err != nil {
			if d.Logger != nil {
				d.Logger.WarnContext(ctx, "delivery metrics: reading the event log failed", "error", err)
			}
			return cursor
		}
		for _, e := range events {
			d.observe(ctx, e)
			cursor = e.Seq
		}
		if len(events) < 500 {
			return cursor
		}
	}
}

func (d *Delivery) observe(ctx context.Context, e event.Event) {
	var p struct {
		Stage    string `json:"stage"`
		Outcome  string `json:"outcome"`
		For      string `json:"for"`
		Status   string `json:"status"`
		Question string `json:"question"`
		Blocking bool   `json:"blocking"`
		By       string `json:"by"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	customer, project := d.key(ctx, e.Customer, "customer"), d.key(ctx, e.Project, "project")
	o := d.Observer
	switch e.Type {
	case "flow.stage_finished":
		o.StageFinished(customer, project, p.Stage, p.Outcome)
	case "flow.done", "flow.failed", "flow.stopped":
		status := e.Type[len("flow."):]
		var lead time.Duration
		iterations := 0
		if f, err := d.Flows.Flow(ctx, e.EntityID); err == nil {
			lead, iterations = e.OccurredAt.Sub(f.StartedAt), f.Iteration
		}
		o.FlowFinished(customer, project, status, lead, iterations)
	case "flow.waiting":
		o.FlowWaiting(customer, project, p.For)
	case "run.finished":
		r, err := d.Runs.Run(ctx, e.EntityID)
		if err != nil {
			return
		}
		var took time.Duration
		if !r.StartedAt.IsZero() {
			took = r.FinishedAt.Sub(r.StartedAt)
		}
		o.RunFinished(customer, project, r.Stage, string(orStatus(run.Status(p.Status), r.Status)), took)
	case "item.question_raised":
		o.QuestionRaised(customer, project, p.Blocking)
	case "item.question_answered":
		q, err := d.Reports.Question(ctx, p.Question)
		if err != nil || q.Status != report.QuestionAnswered {
			return
		}
		by := "human"
		if p.By == "planner" {
			by = "planner"
		}
		o.QuestionAnswered(customer, project, by, q.AnsweredAt.Sub(q.CreatedAt))
	}
}

func orStatus(s, fallback run.Status) run.Status {
	if s != "" {
		return s
	}
	return fallback
}

// key returns the key of a customer or project ID, cached.
func (d *Delivery) key(ctx context.Context, id, kind string) string {
	if id == "" {
		return ""
	}
	d.mu.Lock()
	k, ok := d.keys[id]
	d.mu.Unlock()
	if ok {
		return k
	}
	if kind == "project" {
		if p, err := d.Tenancy.ProjectByID(ctx, id); err == nil {
			k = p.Key
		}
	} else if c, err := d.Tenancy.CustomerByID(ctx, id); err == nil {
		k = c.Key
	}
	if k == "" {
		return id
	}
	d.mu.Lock()
	if d.keys == nil {
		d.keys = map[string]string{}
	}
	d.keys[id] = k
	d.mu.Unlock()
	return k
}

// GaugeRow is a count of things in a state, by customer and project key.
type GaugeRow struct {
	Customer, Project string
	State, Detail     string // e.g. a flow's status and what it waits for
	Count             int
}

// DeliveryGauges is the current state of delivery.
type DeliveryGauges struct {
	Flows     []GaugeRow // active flows: status, waiting
	Questions []GaugeRow // open questions: route
	Runs      []GaugeRow // queued and active runs: status
}

// GaugeStore reads the current state of delivery.
type GaugeStore interface {
	DeliveryGauges(ctx context.Context) (DeliveryGauges, error)
}
