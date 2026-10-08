package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/auth"
)

// FlowStatus is where a ticket's flow through its pipeline stands.
type FlowStatus string

// Flow statuses.
const (
	FlowRunning FlowStatus = "running" // a stage is executing
	FlowWaiting FlowStatus = "waiting" // for an answer, a human, checks or a merge (see Waiting)
	FlowDone    FlowStatus = "done"
	FlowFailed  FlowStatus = "failed"
	FlowStopped FlowStatus = "stopped" // a human paused or cancelled the ticket
)

// Flow is a ticket's way through its pipeline (ADR-0014, ADR-0016).
type Flow struct {
	TicketID        string
	ProjectID       string
	Pipeline        string
	PipelineVersion int64
	Definition      pipeline.Definition // snapshot: the ticket runs on the version it started with
	Stage           string
	Iteration       int // loops back so far
	Status          FlowStatus
	Waiting         string // question, approval, checks, review, merge
	RunID           string // the current stage's agent run
	Outcome         string // of the previous stage
	Report          string // the previous stage's report: the artifact handed to the next stage
	StartedAt       time.Time
	UpdatedAt       time.Time
	Version         int64
}

// FlowStore persists flows; a transition is one atomic batch.
type FlowStore interface {
	SaveFlow(ctx context.Context, f Flow, expectedVersion int64, ticket *ItemWrite, jobs []Job, events []event.Event) error
	Flow(ctx context.Context, ticketID string) (Flow, error)
	ActiveFlows(ctx context.Context) ([]Flow, error)
}

// Job kinds of flows.
const (
	JobFlowEnter    = "flow.enter"    // start the current stage
	JobFlowFinished = "flow.finished" // the current stage's run ended
	JobFlowMerge    = "flow.merge"    // check and merge the pull request
	JobFlowResume   = "flow.resume"   // questions were answered: continue
)

type flowJob struct {
	TicketID    string `json:"ticket_id"`
	FlowVersion int64  `json:"flow_version,omitempty"`
	RunID       string `json:"run_id,omitempty"`
}

// Flows runs tickets through their pipelines: separate agent sessions per
// stage, transitions by outcome, platform and human stages (ADR-0014).
type Flows struct {
	Store        FlowStore
	Items        ItemStore
	Tenancy      TenancyStore
	Authz        Authorizer
	Pipelines    *Pipelines
	Runs         *Runs
	RunStore     RunStore
	Reports      ReportStore
	PullRequests *PullRequests
	Orchestrator *Orchestrator
	Now          func() time.Time
	NewID        func() string
	Logger       *slog.Logger
	// CheckInterval between checks of a pull request waiting for checks,
	// approval or merge; default 30 s.
	CheckInterval time.Duration
	// Budget, when set, returns the budget a ticket's work ran out of
	// (Budgets.CheckTicket): agent stages then wait.
	Budget func(ctx context.Context, it tracker.Item) (*Exceeded, error)
	// Paused, when set, reports whether autonomous work in a project is
	// paused (Control.Paused): flows then wait before their next stage.
	Paused func(ctx context.Context, projectID string) bool
	// Changed, when set, is called after flows move on, which may free a
	// scheduler slot or unblock tickets (Scheduler.Kick).
	Changed func()
}

// FlowView is a flow with its ticket's key.
type FlowView struct {
	Flow
	TicketKey string
}

// Register installs the flow job handlers on the orchestrator.
func (fl *Flows) Register(o *Orchestrator) {
	o.Handle(JobFlowEnter, fl.handleEnter)
	o.Handle(JobFlowFinished, fl.handleFinished)
	o.Handle(JobFlowMerge, fl.handleMerge)
	o.Handle(JobFlowResume, fl.handleResume)
}

func (fl *Flows) job(kind, dedupe string, p flowJob, at time.Time) Job {
	return NewJob(fl.NewID(), kind, dedupe, p, at, 10)
}

func (fl *Flows) itemEvent(it tracker.Item, customerID, typ string, actor event.Actor, payload map[string]any) event.Event {
	return event.Event{Customer: customerID, Project: it.ProjectID, EntityType: "item", EntityID: it.ID, Type: typ,
		Actor: actor, OccurredAt: fl.Now(), Payload: mustJSON(payload)}
}

func (fl *Flows) customer(ctx context.Context, projectID string) (tenancy.Project, tenancy.Customer, error) {
	p, err := fl.Tenancy.ProjectByID(ctx, projectID)
	if err != nil {
		return tenancy.Project{}, tenancy.Customer{}, err
	}
	c, err := fl.Tenancy.CustomerByID(ctx, p.CustomerID)
	return p, c, err
}

// ticketWrite moves a ticket to state (and stage) as the orchestrator.
func (fl *Flows) ticketWrite(it tracker.Item, state tracker.State, stage string) *ItemWrite {
	next := it
	next.State, next.Stage, next.UpdatedAt, next.Version = state, stage, fl.Now(), it.Version+1
	return &ItemWrite{Item: next, ExpectedVersion: it.Version}
}

// Start starts a ready ticket's pipeline. Needs run.manage (the scheduler
// starts runnable tickets on its own).
func (fl *Flows) Start(ctx context.Context, ticketKey string) (FlowView, error) {
	id, err := caller(ctx)
	if err != nil {
		return FlowView{}, err
	}
	it, err := fl.Items.ItemByKey(ctx, ticketKey)
	if err != nil {
		return FlowView{}, err
	}
	p, c, err := fl.customer(ctx, it.ProjectID)
	if err != nil {
		return FlowView{}, err
	}
	if err := fl.Authz.Authorize(ctx, id, ActRunManage, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return FlowView{}, err
	}
	f, err := fl.start(ctx, it, c, actorIn(ctx, id))
	if err != nil {
		return FlowView{}, err
	}
	return FlowView{Flow: f, TicketKey: it.Key}, nil
}

// StartTicket starts a ticket's pipeline for Core's scheduler.
func (fl *Flows) StartTicket(ctx context.Context, it tracker.Item) error {
	_, c, err := fl.customer(ctx, it.ProjectID)
	if err != nil {
		return err
	}
	_, err = fl.start(ctx, it, c, event.System)
	return err
}

func (fl *Flows) start(ctx context.Context, it tracker.Item, c tenancy.Customer, actor event.Actor) (Flow, error) {
	if it.Kind != tracker.KindTicket || it.State != tracker.StateReady {
		return Flow{}, fmt.Errorf("%w: only ready tickets start their pipeline (%s is %s)", ErrConflict, it.Key, it.State)
	}
	prev, err := fl.Store.Flow(ctx, it.ID)
	switch {
	case err == nil && (prev.Status == FlowRunning || prev.Status == FlowWaiting):
		return Flow{}, fmt.Errorf("%w: %s is already in its pipeline", ErrConflict, it.Key)
	case err != nil && !errors.Is(err, ErrNotFound):
		return Flow{}, err
	}
	pv, err := fl.Pipelines.ForTicket(ctx, it.ProjectID, string(it.Type))
	if err != nil {
		return Flow{}, err
	}
	now := fl.Now()
	first := pv.Definition.Stages[0]
	f := Flow{TicketID: it.ID, ProjectID: it.ProjectID, Pipeline: pv.Name, PipelineVersion: pv.Version,
		Definition: pv.Definition, Stage: first.ID, Status: FlowRunning, StartedAt: now, UpdatedAt: now, Version: prev.Version + 1}
	e := fl.itemEvent(it, c.ID, "flow.started", actor, map[string]any{"pipeline": pv.Name, "version": pv.Version, "stage": first.ID})
	j := fl.job(JobFlowEnter, "", flowJob{TicketID: it.ID, FlowVersion: f.Version}, now)
	if err := fl.Store.SaveFlow(ctx, f, prev.Version, fl.ticketWrite(it, tracker.StateInProgress, first.ID), []Job{j}, []event.Event{e}); err != nil {
		return Flow{}, err
	}
	fl.kick()
	return f, nil
}

func (fl *Flows) kick() {
	if fl.Orchestrator != nil {
		fl.Orchestrator.Kick()
	}
	if fl.Changed != nil {
		fl.Changed()
	}
}

// Get returns a ticket's flow (tracker.read).
func (fl *Flows) Get(ctx context.Context, ticketKey string) (FlowView, error) {
	id, err := caller(ctx)
	if err != nil {
		return FlowView{}, err
	}
	it, err := fl.Items.ItemByKey(ctx, ticketKey)
	if err != nil {
		return FlowView{}, err
	}
	p, c, err := fl.customer(ctx, it.ProjectID)
	if err != nil {
		return FlowView{}, err
	}
	if err := fl.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return FlowView{}, err
	}
	f, err := fl.Store.Flow(ctx, it.ID)
	if err != nil {
		return FlowView{}, err
	}
	return FlowView{Flow: f, TicketKey: it.Key}, nil
}

// Decide approves or rejects a ticket waiting in a human stage. Needs
// tracker.write and a human caller.
func (fl *Flows) Decide(ctx context.Context, ticketKey string, approve bool, comment string) (FlowView, error) {
	id, err := caller(ctx)
	if err != nil {
		return FlowView{}, err
	}
	if _, planner := PlannerSessionOf(ctx); planner || id.Kind != auth.KindHuman {
		return FlowView{}, fmt.Errorf("%w: only humans decide human stages", ErrForbidden)
	}
	it, err := fl.Items.ItemByKey(ctx, ticketKey)
	if err != nil {
		return FlowView{}, err
	}
	p, c, err := fl.customer(ctx, it.ProjectID)
	if err != nil {
		return FlowView{}, err
	}
	if err := fl.Authz.Authorize(ctx, id, ActTrackerWrite, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return FlowView{}, err
	}
	f, err := fl.Store.Flow(ctx, it.ID)
	if err != nil {
		return FlowView{}, err
	}
	st, _ := f.Definition.Stage(f.Stage)
	if f.Status != FlowWaiting || st.Kind != pipeline.KindHuman {
		return FlowView{}, fmt.Errorf("%w: %s is not waiting for a human decision", ErrConflict, it.Key)
	}
	outcome, verdict := pipeline.OutcomeDone, "approved"
	if !approve {
		outcome, verdict = pipeline.OutcomeFailed, "rejected"
	}
	text := fmt.Sprintf("%s %s the %s stage.", id.Subject, verdict, f.Stage)
	if strings.TrimSpace(comment) != "" {
		text += "\n\n" + comment
	}
	next, err := fl.transition(ctx, f, it, c, outcome, text, actorIn(ctx, id))
	if err != nil {
		return FlowView{}, err
	}
	return FlowView{Flow: next, TicketKey: it.Key}, nil
}

// RunFinished continues the flow whose current run ended (Dispatcher
// hook).
func (fl *Flows) RunFinished(ctx context.Context, r run.Run) {
	f, err := fl.Store.Flow(ctx, r.TicketID)
	// A run can end before the flow recorded it (handleEnter saves the
	// flow after queuing the run): enqueue for every run of the current
	// stage; the job sorts it out.
	if err != nil || f.Status != FlowRunning || r.CreatedBy != "ballet" || r.Stage != f.Stage {
		return
	}
	j := fl.job(JobFlowFinished, "flow.finished:"+r.ID, flowJob{TicketID: r.TicketID, RunID: r.ID}, fl.Now())
	if err := fl.Orchestrator.Enqueue(ctx, j); err != nil {
		fl.logger().ErrorContext(ctx, "enqueue flow.finished failed", "run", r.ID, "error", err)
	}
}

// current loads a job's flow and ticket; ok is false when the job is
// stale or the ticket left the pipeline (the flow is then stopped).
func (fl *Flows) current(ctx context.Context, p flowJob) (Flow, tracker.Item, tenancy.Customer, bool, error) {
	f, err := fl.Store.Flow(ctx, p.TicketID)
	if err != nil {
		return Flow{}, tracker.Item{}, tenancy.Customer{}, false, err
	}
	if (p.FlowVersion != 0 && f.Version != p.FlowVersion) || (f.Status != FlowRunning && f.Status != FlowWaiting) {
		return f, tracker.Item{}, tenancy.Customer{}, false, nil
	}
	it, err := fl.Items.ItemByID(ctx, f.TicketID)
	if err != nil {
		return Flow{}, tracker.Item{}, tenancy.Customer{}, false, err
	}
	_, c, err := fl.customer(ctx, f.ProjectID)
	if err != nil {
		return Flow{}, tracker.Item{}, tenancy.Customer{}, false, err
	}
	if it.State != tracker.StateInProgress && it.State != tracker.StateWaitingForAnswer {
		return f, it, c, false, fl.stop(ctx, f, it, c)
	}
	return f, it, c, true, nil
}

// stop ends a flow whose ticket a human took out of the pipeline, and
// cancels its active run.
func (fl *Flows) stop(ctx context.Context, f Flow, it tracker.Item, c tenancy.Customer) error {
	fl.cancelRun(ctx, f.RunID)
	next := f
	next.Status, next.Waiting, next.UpdatedAt, next.Version = FlowStopped, "", fl.Now(), f.Version+1
	e := fl.itemEvent(it, c.ID, "flow.stopped", event.System, map[string]any{"stage": f.Stage, "state": it.State})
	if err := fl.Store.SaveFlow(ctx, next, f.Version, nil, nil, []event.Event{e}); err != nil {
		return ignoreConflict(err)
	}
	fl.kick()
	return nil
}

// cancelRun cancels a flow's run unless it already ended.
func (fl *Flows) cancelRun(ctx context.Context, runID string) {
	if runID == "" || fl.Runs.Dispatcher == nil {
		return
	}
	// The dispatcher may move the run on meanwhile: retry from a fresh read.
	for attempt := 0; attempt < 3; attempt++ {
		r, err := fl.RunStore.Run(ctx, runID)
		if err != nil || r.Status.Terminal() {
			return
		}
		_, err = fl.Runs.Dispatcher.cancel(ctx, r, event.System)
		if err == nil {
			return
		}
		if !errors.Is(err, ErrConflict) || attempt == 2 {
			fl.logger().WarnContext(ctx, "cancel run failed", "run", r.ID, "error", err)
			return
		}
	}
}

// ignoreConflict treats losing a race as success: another transition
// moved the flow on. Job handlers instead return conflicts, so the job is
// retried from a fresh read (stale retries are no-ops).
func ignoreConflict(err error) error {
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

func decode(j Job) (flowJob, error) {
	var p flowJob
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return flowJob{}, fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	return p, nil
}

// handleEnter starts the flow's current stage.
func (fl *Flows) handleEnter(ctx context.Context, j Job) error {
	p, err := decode(j)
	if err != nil {
		return err
	}
	f, it, c, ok, err := fl.current(ctx, p)
	if err != nil || !ok {
		return err
	}
	if fl.paused(ctx, f.ProjectID) {
		return fl.hold(ctx, f, it, c, "pause")
	}
	st, found := f.Definition.Stage(f.Stage)
	if !found {
		return fmt.Errorf("%w: stage %s is not in the pipeline", ErrPermanent, f.Stage)
	}
	next := f
	next.UpdatedAt, next.Version = fl.Now(), f.Version+1
	var jobs []Job
	var events []event.Event
	switch st.Kind {
	case pipeline.KindAgent:
		if done, err := fl.overBudget(ctx, f, it, c); done || err != nil {
			return err
		}
		r, err := fl.stageRun(ctx, f, it, st)
		if err != nil {
			return err
		}
		next.Status, next.Waiting, next.RunID = FlowRunning, "", r.ID
		events = append(events, fl.itemEvent(it, c.ID, "flow.stage_started", event.System,
			map[string]any{"stage": st.ID, "run": r.ID, "iteration": f.Iteration}))
	case pipeline.KindHuman:
		next.Status, next.Waiting = FlowWaiting, "approval"
		events = append(events, fl.itemEvent(it, c.ID, "flow.waiting", event.System, map[string]any{"stage": st.ID, "for": "approval"}))
	case pipeline.KindPlatform:
		next.Status, next.Waiting = FlowWaiting, "merge"
		jobs = append(jobs, fl.job(JobFlowMerge, "", flowJob{TicketID: it.ID, FlowVersion: next.Version}, fl.Now()))
		events = append(events, fl.itemEvent(it, c.ID, "flow.stage_started", event.System, map[string]any{"stage": st.ID, "action": st.Action}))
	}
	if err := fl.Store.SaveFlow(ctx, next, f.Version, nil, jobs, events); err != nil {
		return err
	}
	fl.kick()
	return nil
}

// stageRun queues the stage's agent run, or finds the one queued by an
// earlier attempt of this job.
func (fl *Flows) stageRun(ctx context.Context, f Flow, it tracker.Item, st pipeline.Stage) (run.Run, error) {
	runs, err := fl.RunStore.ListRuns(ctx, RunFilter{TicketID: it.ID})
	if err != nil {
		return run.Run{}, err
	}
	for _, r := range runs {
		if r.Stage == st.ID && !r.CreatedAt.Before(f.UpdatedAt) && r.CreatedBy == "ballet" {
			return r, nil
		}
	}
	adapter := st.Adapter
	if adapter == "" {
		adapter = pipeline.DefaultAdapter
	}
	v, err := fl.Runs.CreateForStage(ctx, it, st.ID, adapter, StageOptions{
		Instructions: st.Instructions, Skills: st.Skills, Model: st.Model, Artifacts: f.Report,
	}, st.TimeoutMinutes*60)
	return v.Run, err
}

// handleFinished turns the end of the current stage's run into the
// stage's outcome.
func (fl *Flows) handleFinished(ctx context.Context, j Job) error {
	p, err := decode(j)
	if err != nil {
		return err
	}
	f, it, c, ok, err := fl.current(ctx, flowJob{TicketID: p.TicketID})
	if err != nil || !ok {
		return err
	}
	if f.RunID != p.RunID {
		if f.RunID == "" && f.Status == FlowRunning {
			return errors.New("the flow has not recorded its run yet") // retried
		}
		return nil // a run the flow moved past
	}
	r, err := fl.RunStore.Run(ctx, p.RunID)
	if err != nil {
		return err
	}
	if r.Status == run.StatusCancelled && fl.paused(ctx, f.ProjectID) {
		// The kill switch stopped it: the stage runs again on resume.
		return fl.hold(ctx, f, it, c, "pause")
	}
	if r.Status != run.StatusSucceeded {
		// The gateway refuses calls over budget: a stage that failed then
		// waits for budget instead of failing.
		if done, err := fl.overBudget(ctx, f, it, c); done || err != nil {
			return err
		}
	}
	outcome, text := fl.stageOutcome(ctx, r)
	asked, err := fl.blockingQuestions(ctx, it.ID, r.ID)
	if err != nil {
		return err
	}
	if waitsForAnswers(r, asked, outcome) {
		// The session ended to wait for answers (ADR-0015, ADR-0026): the
		// stage continues in a new session once they are answered.
		return fl.awaitAnswers(ctx, f, it, c, r, text)
	}
	_, err = fl.transition(ctx, f, it, c, outcome, text, event.System)
	return err
}

// overBudget holds the flow when its ticket's work ran out of budget: a
// used-up ticket budget asks the humans; a daily budget waits for the
// next day (or a raised budget). done reports whether it held the flow.
func (fl *Flows) overBudget(ctx context.Context, f Flow, it tracker.Item, c tenancy.Customer) (bool, error) {
	if fl.Budget == nil {
		return false, nil
	}
	ex, err := fl.Budget(ctx, it)
	if err != nil || ex == nil {
		return false, err
	}
	if ex.Scope != "ticket" {
		return true, fl.hold(ctx, f, it, c, "budget")
	}
	next := f
	next.Status, next.Waiting, next.RunID, next.UpdatedAt, next.Version = FlowWaiting, "question", "", fl.Now(), f.Version+1
	q := report.Question{ID: fl.NewID(), ProjectID: it.ProjectID, TicketID: it.ID,
		Text: fmt.Sprintf("%s ran out of budget before its %s stage. Raise the budget, then answer to continue.", it.Key,
			f.Stage), Context: ex.Reason(), Blocking: true, Status: report.QuestionOpen, Route: report.RouteHuman,
		CreatedAt: fl.Now()}
	qe := fl.itemEvent(it, c.ID, "item.question_raised", event.System, map[string]any{"question": q.ID, "blocking": true})
	if err := fl.Reports.CreateQuestion(ctx, q, qe); err != nil {
		return true, err
	}
	e := fl.itemEvent(it, c.ID, "flow.waiting", event.System, map[string]any{"stage": f.Stage, "for": "budget",
		"used": ex.Used, "limit": ex.Limit})
	if err := fl.Store.SaveFlow(ctx, next, f.Version, fl.ticketWrite(it, tracker.StateWaitingForAnswer, f.Stage), nil,
		[]event.Event{e}); err != nil {
		return true, err
	}
	fl.kick()
	return true, nil
}

// withinBudget reports whether a flow held for budget may continue.
func (fl *Flows) withinBudget(ctx context.Context, f Flow) bool {
	if fl.Budget == nil {
		return true
	}
	it, err := fl.Items.ItemByID(ctx, f.TicketID)
	if err != nil {
		return false
	}
	ex, err := fl.Budget(ctx, it)
	return err == nil && ex == nil
}

func (fl *Flows) paused(ctx context.Context, projectID string) bool {
	return fl.Paused != nil && fl.Paused(ctx, projectID)
}

// hold makes a flow wait before its current stage — for a pause to end
// ("pause") or for budget ("budget"); the stage then starts (again).
func (fl *Flows) hold(ctx context.Context, f Flow, it tracker.Item, c tenancy.Customer, what string) error {
	next := f
	next.Status, next.Waiting, next.RunID, next.UpdatedAt, next.Version = FlowWaiting, what, "", fl.Now(), f.Version+1
	e := fl.itemEvent(it, c.ID, "flow.waiting", event.System, map[string]any{"stage": f.Stage, "for": what})
	if err := fl.Store.SaveFlow(ctx, next, f.Version, nil, nil, []event.Event{e}); err != nil {
		return err
	}
	fl.kick()
	return nil
}

// ResumePaused continues the flows waiting for a pause that has ended.
func (fl *Flows) ResumePaused(ctx context.Context) {
	flows, err := fl.Store.ActiveFlows(ctx)
	if err != nil {
		fl.logger().ErrorContext(ctx, "resuming paused flows failed", "error", err)
		return
	}
	for _, f := range flows {
		if f.Waiting != "pause" || fl.paused(ctx, f.ProjectID) {
			continue
		}
		if err := fl.unpause(ctx, f); err != nil && !errors.Is(err, ErrConflict) {
			fl.logger().ErrorContext(ctx, "resuming a paused flow failed", "ticket", f.TicketID, "error", err)
		}
	}
}

func (fl *Flows) unpause(ctx context.Context, f Flow) error {
	it, err := fl.Items.ItemByID(ctx, f.TicketID)
	if err != nil {
		return err
	}
	_, c, err := fl.customer(ctx, f.ProjectID)
	if err != nil {
		return err
	}
	next := f
	next.Status, next.Waiting, next.UpdatedAt, next.Version = FlowRunning, "", fl.Now(), f.Version+1
	e := fl.itemEvent(it, c.ID, "flow.resumed", event.System, map[string]any{"stage": f.Stage, "after": f.Waiting})
	jobs := []Job{fl.job(JobFlowEnter, "", flowJob{TicketID: it.ID, FlowVersion: next.Version}, fl.Now())}
	if err := fl.Store.SaveFlow(ctx, next, f.Version, nil, jobs, []event.Event{e}); err != nil {
		return err
	}
	fl.kick()
	return nil
}

// waitsForAnswers reports whether a stage run ended to wait for answers
// to the blocking questions it asked: it parked, or one is still open, or
// it reported itself blocked. A session that got its answers online and
// went on to finish its stage does not wait.
func waitsForAnswers(r run.Run, asked []report.Question, outcome pipeline.Outcome) bool {
	if len(asked) == 0 {
		return false
	}
	if r.Result != nil && r.Result.Parked || outcome == pipeline.OutcomeBlocked {
		return true
	}
	for _, q := range asked {
		if q.Status == report.QuestionOpen {
			return true
		}
	}
	return false
}

// blockingQuestions returns the blocking questions a run asked.
func (fl *Flows) blockingQuestions(ctx context.Context, ticketID, runID string) ([]report.Question, error) {
	qs, err := fl.Reports.Questions(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	var out []report.Question
	for _, q := range qs {
		if q.Blocking && q.RunID == runID {
			out = append(out, q)
		}
	}
	return out, nil
}

// awaitAnswers makes the flow wait for the answers to the questions its
// stage's run asked; when they are answered already, it resumes at once.
func (fl *Flows) awaitAnswers(ctx context.Context, f Flow, it tracker.Item, c tenancy.Customer, r run.Run, text string) error {
	next := f
	next.Status, next.Waiting, next.RunID, next.Outcome = FlowWaiting, "question", "", ""
	next.Report = fmt.Sprintf("Report of the %s stage so far (it asked questions):\n\n%s", f.Stage, strings.TrimSpace(text))
	next.UpdatedAt, next.Version = fl.Now(), f.Version+1
	e := fl.itemEvent(it, c.ID, "flow.waiting", event.System, map[string]any{"stage": f.Stage, "for": "answer", "run": r.ID})
	jobs := []Job{fl.job(JobFlowResume, "", flowJob{TicketID: it.ID}, fl.Now())}
	if err := fl.Store.SaveFlow(ctx, next, f.Version, fl.ticketWrite(it, tracker.StateWaitingForAnswer, f.Stage), jobs,
		[]event.Event{e}); err != nil {
		return err
	}
	fl.kick()
	return nil
}

// handleResume continues a flow waiting for answers once no blocking
// question of its ticket is open: the stage starts again in a new session
// with the questions and answers handed over.
func (fl *Flows) handleResume(ctx context.Context, j Job) error {
	p, err := decode(j)
	if err != nil {
		return err
	}
	f, it, c, ok, err := fl.current(ctx, flowJob{TicketID: p.TicketID})
	if errors.Is(err, ErrNotFound) {
		return nil // the question came from a run outside a pipeline
	}
	if err != nil || !ok || f.Status != FlowWaiting || f.Waiting != "question" {
		return err
	}
	qs, err := fl.Reports.Questions(ctx, it.ID)
	if err != nil {
		return err
	}
	var answered strings.Builder
	for _, q := range qs {
		if q.Status == report.QuestionOpen && q.Blocking {
			return nil // a later answer resumes
		}
		if q.Status == report.QuestionAnswered && !q.AnsweredAt.Before(f.StartedAt) {
			fmt.Fprintf(&answered, "\n\n**Question:** %s\n\n**Answer** (%s): %s", q.Text, q.AnsweredBy, q.Answer)
		}
	}
	next := f
	next.Status, next.Waiting, next.RunID, next.UpdatedAt, next.Version = FlowRunning, "", "", fl.Now(), f.Version+1
	if answered.Len() > 0 {
		next.Report = strings.TrimSpace(f.Report + "\n\n## Answered questions" + answered.String())
	}
	if next.Iteration > next.Definition.MaxIterations {
		next.Iteration = 0 // a human decided how to go on: a fresh loop budget
	}
	e := fl.itemEvent(it, c.ID, "flow.resumed", event.System, map[string]any{"stage": f.Stage})
	jobs := []Job{fl.job(JobFlowEnter, "", flowJob{TicketID: it.ID, FlowVersion: next.Version}, fl.Now())}
	if err := fl.Store.SaveFlow(ctx, next, f.Version, fl.ticketWrite(it, tracker.StateInProgress, f.Stage), jobs,
		[]event.Event{e}); err != nil {
		return err
	}
	fl.kick()
	return nil
}

// stageOutcome is the outcome of a stage run: the agent's stage report,
// else the run's status.
func (fl *Flows) stageOutcome(ctx context.Context, r run.Run) (pipeline.Outcome, string) {
	reports, err := fl.Reports.Reports(ctx, r.TicketID, r.ID)
	if err == nil {
		for i := len(reports) - 1; i >= 0; i-- {
			if rep := reports[i]; rep.Kind == report.KindStageReport {
				text := rep.Text
				if rep.Detail != "" {
					text += "\n\n" + rep.Detail
				}
				return pipeline.Outcome(rep.Outcome), text
			}
		}
	}
	summary := ""
	if r.Result != nil {
		summary = r.Result.Summary
	}
	if r.Status == run.StatusSucceeded {
		return pipeline.OutcomeDone, summary
	}
	return pipeline.OutcomeFailed, strings.TrimSpace(r.Error + "\n\n" + summary)
}

// transition moves the flow on from its current stage by outcome.
func (fl *Flows) transition(ctx context.Context, f Flow, it tracker.Item, c tenancy.Customer, outcome pipeline.Outcome,
	text string, actor event.Actor) (Flow, error) {
	target := f.Definition.Target(f.Stage, outcome)
	next := f
	next.Outcome, next.RunID, next.Waiting, next.UpdatedAt, next.Version = string(outcome), "", "", fl.Now(), f.Version+1
	next.Report = fmt.Sprintf("Report of the %s stage (%s):\n\n%s", f.Stage, outcome, strings.TrimSpace(text))
	reason := ""
	if !strings.HasPrefix(target, "$") && f.Definition.IsLoop(f.Stage, target) {
		next.Iteration++
		if next.Iteration > f.Definition.MaxIterations {
			// After the answer the flow goes on where the loop went.
			next.Stage = target
			target, reason = pipeline.TargetQuestion,
				fmt.Sprintf("The pipeline went back %d times, more than its limit of %d.", next.Iteration, f.Definition.MaxIterations)
		}
	}
	events := []event.Event{fl.itemEvent(it, c.ID, "flow.stage_finished", actor,
		map[string]any{"stage": f.Stage, "outcome": outcome, "next": target})}
	var ticket *ItemWrite
	var jobs []Job
	switch target {
	case pipeline.TargetDone:
		next.Status = FlowDone
		ticket = fl.ticketWrite(it, tracker.StateDone, "")
		events = append(events, fl.itemEvent(it, c.ID, "flow.done", actor, nil))
	case pipeline.TargetFailed:
		next.Status = FlowFailed
		ticket = fl.ticketWrite(it, tracker.StatePaused, "")
		events = append(events, fl.itemEvent(it, c.ID, "flow.failed", actor, map[string]any{"stage": f.Stage}))
	case pipeline.TargetQuestion:
		next.Status, next.Waiting = FlowWaiting, "question"
		ticket = fl.ticketWrite(it, tracker.StateWaitingForAnswer, next.Stage)
		// Questions about the process go to the humans directly.
		q := report.Question{ID: fl.NewID(), ProjectID: it.ProjectID, TicketID: it.ID,
			Text:    fmt.Sprintf("The %s stage of %s ended %s. How should it continue?", f.Stage, it.Key, outcome),
			Context: strings.TrimSpace(reason + "\n\n" + next.Report), Blocking: true, Status: report.QuestionOpen,
			Route: report.RouteHuman, CreatedAt: fl.Now()}
		qe := fl.itemEvent(it, c.ID, "item.question_raised", event.System, map[string]any{"question": q.ID, "blocking": true})
		if err := fl.Reports.CreateQuestion(ctx, q, qe); err != nil {
			return Flow{}, err
		}
	default:
		next.Status, next.Stage = FlowRunning, target
		ticket = fl.ticketWrite(it, tracker.StateInProgress, target)
		jobs = append(jobs, fl.job(JobFlowEnter, "", flowJob{TicketID: it.ID, FlowVersion: next.Version}, fl.Now()))
	}
	if err := fl.Store.SaveFlow(ctx, next, f.Version, ticket, jobs, events); err != nil {
		return Flow{}, err
	}
	fl.kick()
	return next, nil
}

// handleMerge drives a merge stage: open the pull request, wait for
// checks and the approvals the ticket's policy requires, merge when it
// allows (ADR-0008).
func (fl *Flows) handleMerge(ctx context.Context, j Job) error {
	p, err := decode(j)
	if err != nil {
		return err
	}
	f, it, c, ok, err := fl.current(ctx, p)
	if err != nil || !ok {
		return err
	}
	if fl.paused(ctx, f.ProjectID) {
		return fl.hold(ctx, f, it, c, "pause")
	}
	pr, err := fl.PullRequests.EnsureForTicket(ctx, it)
	switch {
	case errors.Is(err, forge.ErrNoBranch):
		_, err := fl.transition(ctx, f, it, c, pipeline.OutcomeFailed,
			"The ticket branch was not pushed: there is nothing to integrate.", event.System)
		return err
	case errors.Is(err, ErrInvalid):
		_, err := fl.transition(ctx, f, it, c, pipeline.OutcomeBlocked, err.Error(), event.System)
		return err
	case err != nil:
		return err
	}
	fail := func(text string) error {
		_, err := fl.transition(ctx, f, it, c, pipeline.OutcomeFailed, text+" ("+pr.URL+")", event.System)
		return err
	}
	switch {
	case pr.State == forge.StateMerged:
		_, err := fl.transition(ctx, f, it, c, pipeline.OutcomeDone, "Merged: "+pr.URL, event.System)
		return err
	case pr.State == forge.StateClosed:
		return fail("The pull request was closed without merging.")
	case pr.Checks == forge.ChecksFailure:
		return fail("Checks failed on the pull request.")
	case pr.Review == forge.ReviewChangesRequested:
		return fail("Changes were requested on the pull request.")
	case pr.Checks == forge.ChecksPending:
		return fl.wait(ctx, f, it, c, "checks")
	case it.Policy.ReviewMode == tracker.ReviewAgentHuman && pr.Review != forge.ReviewApproved:
		return fl.wait(ctx, f, it, c, "review")
	case it.Policy.MergeMode == tracker.MergeManual || pr.Forge == "git":
		return fl.wait(ctx, f, it, c, "merge")
	}
	merged, err := fl.PullRequests.MergeForTicket(ctx, it)
	if err != nil {
		return err
	}
	if merged.State == forge.StateMerged {
		_, err := fl.transition(ctx, f, it, c, pipeline.OutcomeDone, "Merged: "+merged.URL, event.System)
		return err
	}
	return fl.wait(ctx, f, it, c, "merge")
}

// wait records what the merge stage waits for and checks again later.
func (fl *Flows) wait(ctx context.Context, f Flow, it tracker.Item, c tenancy.Customer, what string) error {
	interval := fl.CheckInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	next := f
	next.Status, next.Waiting, next.UpdatedAt, next.Version = FlowWaiting, what, fl.Now(), f.Version+1
	var events []event.Event
	if f.Waiting != what {
		events = append(events, fl.itemEvent(it, c.ID, "flow.waiting", event.System, map[string]any{"stage": f.Stage, "for": what}))
	}
	jobs := []Job{fl.job(JobFlowMerge, "", flowJob{TicketID: it.ID, FlowVersion: next.Version}, fl.Now().Add(interval))}
	return fl.Store.SaveFlow(ctx, next, f.Version, nil, jobs, events)
}

func (fl *Flows) logger() *slog.Logger {
	if fl.Logger != nil {
		return fl.Logger
	}
	return slog.Default()
}
