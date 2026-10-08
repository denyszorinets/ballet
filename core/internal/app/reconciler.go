package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// FlowJobStore finds the jobs of a ticket's flow.
type FlowJobStore interface {
	// FlowJobs returns the ticket's flow jobs that are not done (live and
	// dead ones).
	FlowJobs(ctx context.Context, ticketID string) ([]Job, error)
}

// Reconciler repairs drift between flows, jobs and runs after crashes and
// flags stuck stages (ADR-0016). Each pass:
//
//   - stops flows whose ticket a human took out of the pipeline (and
//     cancels their run);
//   - flags a stage whose run made no progress beyond its timeout: the run
//     is cancelled and a blocking question raised;
//   - re-queues the job a flow waits for when it was lost (Core stopped
//     between finishing a run and queuing its follow-up), or flags the
//     flow when that job failed for good;
//   - cancels stage runs no flow waits for.
type Reconciler struct {
	Flows    *Flows
	Jobs     FlowJobStore
	Now      func() time.Time // default Flows.Now
	Interval time.Duration    // between passes; default 1 minute
	// DefaultTimeout of stage runs without one; default 2 hours (the
	// agent's default).
	DefaultTimeout time.Duration
	// Slack beyond a run's timeout before it counts as stuck, and the
	// minimum age of an orphaned run; default 10 minutes.
	Slack time.Duration
}

// Run reconciles until ctx is cancelled.
func (rc *Reconciler) Run(ctx context.Context) {
	interval := rc.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		if err := rc.Reconcile(ctx); err != nil && ctx.Err() == nil {
			rc.Flows.logger().WarnContext(ctx, "reconciling flows failed", "error", err)
		}
	}
}

// Reconcile makes one pass over the active flows and stage runs.
func (rc *Reconciler) Reconcile(ctx context.Context) error {
	fl := rc.Flows
	flows, err := fl.Store.ActiveFlows(ctx)
	if err != nil {
		return err
	}
	var errs []error
	active := map[string]Flow{}
	for _, f := range flows {
		active[f.TicketID] = f
		if err := rc.flow(ctx, f); err != nil && !errors.Is(err, ErrConflict) {
			errs = append(errs, fmt.Errorf("flow of ticket %s: %w", f.TicketID, err))
		}
	}
	runs, err := fl.RunStore.ListRuns(ctx, RunFilter{Statuses: []run.Status{run.StatusQueued, run.StatusStarting, run.StatusRunning}})
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, r := range runs {
		if r.CreatedBy != "ballet" || r.TicketID == "" || rc.now().Sub(r.CreatedAt) < rc.slack() {
			continue
		}
		f, ok := active[r.TicketID]
		if ok && f.Status == FlowRunning && f.Stage == r.Stage && (f.RunID == r.ID || f.RunID == "") {
			continue // the flow's run, or one it is about to record
		}
		fl.logger().WarnContext(ctx, "cancelling orphaned stage run", "run", r.ID, "stage", r.Stage)
		fl.cancelRun(ctx, r.ID)
	}
	return errors.Join(errs...)
}

func (rc *Reconciler) flow(ctx context.Context, f Flow) error {
	fl := rc.Flows
	it, err := fl.Items.ItemByID(ctx, f.TicketID)
	if err != nil {
		return err
	}
	_, c, err := fl.customer(ctx, f.ProjectID)
	if err != nil {
		return err
	}
	if it.State != tracker.StateInProgress && it.State != tracker.StateWaitingForAnswer {
		return fl.stop(ctx, f, it, c)
	}
	var r run.Run
	if f.RunID != "" {
		if r, err = fl.RunStore.Run(ctx, f.RunID); err != nil {
			return err
		}
		if !r.Status.Terminal() {
			if limit := rc.timeout(r) + rc.slack(); rc.now().Sub(rc.since(r)) > limit {
				return rc.flag(ctx, f, it, c, fmt.Sprintf("The %s stage's run %s made no progress for %s, beyond its timeout.",
					f.Stage, r.ID, limit))
			}
			return nil // the agent (or the dispatcher's sweep) ends it
		}
	}
	if f.Status == FlowWaiting && f.Waiting == "question" {
		return rc.resumeIfAnswered(ctx, f)
	}
	if f.Status == FlowWaiting && f.Waiting == "budget" {
		if fl.paused(ctx, f.ProjectID) || !fl.withinBudget(ctx, f) {
			return nil
		}
		return fl.unpause(ctx, f) // a new day, or a raised budget
	}
	if f.Status == FlowWaiting && f.Waiting == "pause" {
		if fl.paused(ctx, f.ProjectID) {
			return nil
		}
		return fl.unpause(ctx, f) // its resumption was missed
	}
	want, ok := rc.expected(f)
	if !ok {
		return nil
	}
	jobs, err := rc.Jobs.FlowJobs(ctx, f.TicketID)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		var p flowJob
		if json.Unmarshal(j.Payload, &p) != nil {
			continue
		}
		current := (p.FlowVersion != 0 && p.FlowVersion == f.Version) || (p.RunID != "" && p.RunID == f.RunID)
		switch {
		case current && (j.Status == JobPending || j.Status == JobRunning):
			return nil // the job is on its way
		case current && j.Status == JobDead && j.Kind == want.Kind:
			return rc.flag(ctx, f, it, c, fmt.Sprintf("Core could not continue the %s stage: %s", f.Stage, j.LastError))
		}
	}
	fl.logger().WarnContext(ctx, "re-queuing lost flow job", "ticket", it.Key, "kind", want.Kind)
	if err := fl.Orchestrator.Enqueue(ctx, want); err != nil {
		return err
	}
	return nil
}

// resumeIfAnswered queues the resumption of a flow whose blocking
// questions were all answered (its resume job was lost).
func (rc *Reconciler) resumeIfAnswered(ctx context.Context, f Flow) error {
	fl := rc.Flows
	qs, err := fl.Reports.Questions(ctx, f.TicketID)
	if err != nil {
		return err
	}
	for _, q := range qs {
		if q.Status == report.QuestionOpen && q.Blocking {
			return nil
		}
	}
	fl.logger().WarnContext(ctx, "resuming flow whose questions were answered", "ticket", f.TicketID)
	return fl.Orchestrator.Enqueue(ctx, fl.job(JobFlowResume, "flow.resume:"+f.TicketID, flowJob{TicketID: f.TicketID}, fl.Now()))
}

// expected returns the job an active flow waits for, if any.
func (rc *Reconciler) expected(f Flow) (Job, bool) {
	fl := rc.Flows
	switch {
	case f.Status == FlowRunning && f.RunID == "":
		return fl.job(JobFlowEnter, "", flowJob{TicketID: f.TicketID, FlowVersion: f.Version}, fl.Now()), true
	case f.Status == FlowRunning:
		return fl.job(JobFlowFinished, "flow.finished:"+f.RunID, flowJob{TicketID: f.TicketID, RunID: f.RunID}, fl.Now()), true
	case f.Status == FlowWaiting && (f.Waiting == "checks" || f.Waiting == "review" || f.Waiting == "merge"):
		return fl.job(JobFlowMerge, "", flowJob{TicketID: f.TicketID, FlowVersion: f.Version}, fl.Now()), true
	}
	return Job{}, false
}

// flag stops a flow's stage, cancels its run and asks the humans how to
// continue.
func (rc *Reconciler) flag(ctx context.Context, f Flow, it tracker.Item, c tenancy.Customer, reason string) error {
	fl := rc.Flows
	next := f
	next.Status, next.Waiting, next.RunID, next.Report = FlowWaiting, "question", "", reason
	next.UpdatedAt, next.Version = fl.Now(), f.Version+1
	q := report.Question{ID: fl.NewID(), ProjectID: it.ProjectID, TicketID: it.ID,
		Text: fmt.Sprintf("The %s stage of %s is stuck. How should it continue?", f.Stage, it.Key), Context: reason,
		Blocking: true, Status: report.QuestionOpen, Route: report.RouteHuman, CreatedAt: fl.Now()}
	qe := fl.itemEvent(it, c.ID, "item.question_raised", event.System, map[string]any{"question": q.ID, "blocking": true})
	if err := fl.Reports.CreateQuestion(ctx, q, qe); err != nil {
		return err
	}
	e := fl.itemEvent(it, c.ID, "flow.stuck", event.System, map[string]any{"stage": f.Stage, "reason": reason})
	if err := fl.Store.SaveFlow(ctx, next, f.Version, fl.ticketWrite(it, tracker.StateWaitingForAnswer, f.Stage), nil,
		[]event.Event{e}); err != nil {
		return err
	}
	// Only now: the flow no longer waits for the run, so its end is ignored.
	fl.cancelRun(ctx, f.RunID)
	fl.logger().WarnContext(ctx, "flagged stuck stage", "ticket", it.Key, "stage", f.Stage, "reason", reason)
	fl.kick()
	return nil
}

func (rc *Reconciler) now() time.Time {
	if rc.Now != nil {
		return rc.Now()
	}
	return rc.Flows.Now()
}

func (rc *Reconciler) slack() time.Duration {
	if rc.Slack > 0 {
		return rc.Slack
	}
	return 10 * time.Minute
}

func (rc *Reconciler) timeout(r run.Run) time.Duration {
	if r.Spec.TimeoutSeconds > 0 {
		return time.Duration(r.Spec.TimeoutSeconds) * time.Second
	}
	if rc.DefaultTimeout > 0 {
		return rc.DefaultTimeout
	}
	return 2 * time.Hour
}

// since is when a run last made progress we know of: its start, or its
// creation while queued.
func (rc *Reconciler) since(r run.Run) time.Time {
	if !r.StartedAt.IsZero() {
		return r.StartedAt
	}
	return r.CreatedAt
}
