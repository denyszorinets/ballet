package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/agent"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// RunStore persists runs and their output.
type RunStore interface {
	CreateRun(ctx context.Context, r run.Run, e event.Event) error
	UpdateRun(ctx context.Context, r run.Run, expectedVersion int64, e *event.Event) error
	Run(ctx context.Context, id string) (run.Run, error)
	ListRuns(ctx context.Context, f RunFilter) ([]run.Run, error)
	AppendRunLog(ctx context.Context, runID, stream, text string, at time.Time, maxBytes int64) (bool, error)
	RunLogs(ctx context.Context, runID string, afterSeq int64, limit int) ([]RunLog, error)
}

// RunFilter selects runs; zero fields do not filter.
type RunFilter struct {
	TicketID string
	Runner   string
	Statuses []run.Status
	Limit    int
}

// RunLog is a chunk of a run's output.
type RunLog struct {
	Seq    int64
	Stream string
	Text   string
	At     time.Time
}

// RunView is a run with the keys of its ticket and project.
type RunView struct {
	run.Run
	TicketKey  string
	ProjectKey string
}

// Runs implements the human-facing run use cases. Runs are queued for a
// ticket and executed by Runners through the Dispatcher.
type Runs struct {
	Store     RunStore
	Execution ExecutionStore // nil: runs execute their spec as given
	// Agents are the adapters agent runs can use, by name.
	Agents map[string]agent.Adapter
	// SessionSkills returns the skills a project's sessions get.
	SessionSkills func(ctx context.Context, projectKey string) ([]agent.Skill, error)
	MCP           []agent.MCPServer // MCP servers every session gets
	Model         string            // agent model; "": the runtime's default
	Items         ItemStore
	Tenancy       TenancyStore
	Authz         Authorizer
	Dispatcher    *Dispatcher
	Now           func() time.Time
	NewID         func() string
}

// AgentInput describes an agent run.
type AgentInput struct {
	Adapter        string // e.g. "claude-code"
	Prompt         string
	TimeoutSeconds int
}

// CreateAgent queues a run executing a coding agent through an adapter:
// the session gets the prompt, the project's skills and the MCP servers.
// Like Create, it needs run.manage.
func (rs *Runs) CreateAgent(ctx context.Context, ticketKey, stage string, in AgentInput) (RunView, error) {
	a, ok := rs.Agents[in.Adapter]
	if !ok {
		return RunView{}, fmt.Errorf("%w: unknown agent adapter %q", ErrInvalid, in.Adapter)
	}
	return rs.create(ctx, ticketKey, stage, func(it tracker.Item, p tenancy.Project) (run.Spec, string, error) {
		var skills []agent.Skill
		if rs.SessionSkills != nil {
			var err error
			if skills, err = rs.SessionSkills(ctx, p.Key); err != nil {
				return run.Spec{}, "", err
			}
		}
		built, err := a.Build(agent.Session{
			Prompt: in.Prompt, Skills: skills, MCP: rs.MCP, Model: rs.Model,
			Instructions: fmt.Sprintf("You are a coding agent of Ballet working on ticket %s (%s) of project %s, "+
				"stage %q. Work in the current repository; commit and push your work to the current branch.",
				it.Key, it.Title, p.Key, stage),
		})
		if err != nil {
			return run.Spec{}, "", invalid(err)
		}
		return run.Spec{Command: built.Command, Files: built.Files, Env: built.Env, TimeoutSeconds: in.TimeoutSeconds}, a.Name(), nil
	})
}

// Create queues a run of stage for a ticket. Queuing by hand needs
// run.manage on the project (the orchestrator queues runs in M5).
func (rs *Runs) Create(ctx context.Context, ticketKey, stage string, spec run.Spec) (RunView, error) {
	return rs.create(ctx, ticketKey, stage, func(tracker.Item, tenancy.Project) (run.Spec, string, error) {
		return spec, "", nil
	})
}

func (rs *Runs) create(ctx context.Context, ticketKey, stage string,
	build func(tracker.Item, tenancy.Project) (run.Spec, string, error)) (RunView, error) {
	id, err := caller(ctx)
	if err != nil {
		return RunView{}, err
	}
	it, p, c, err := rs.ticket(ctx, ticketKey)
	if err != nil {
		return RunView{}, err
	}
	if err := rs.Authz.Authorize(ctx, id, ActRunManage, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return RunView{}, err
	}
	if it.Kind != tracker.KindTicket {
		return RunView{}, fmt.Errorf("%w: runs execute tickets, not %ss", ErrInvalid, it.Kind)
	}
	spec, adapter, err := build(it, p)
	if err != nil {
		return RunView{}, err
	}
	r := run.Run{ID: rs.NewID(), ProjectID: p.ID, TicketID: it.ID, Stage: stage, Status: run.StatusQueued, Spec: spec,
		Adapter: adapter, CreatedBy: id.Subject, CreatedAt: rs.Now(), Version: 1}
	if err := r.Validate(); err != nil {
		return RunView{}, invalid(err)
	}
	if err := rs.prepare(ctx, &r, it); err != nil {
		return RunView{}, err
	}
	e := runEvent(r, c.ID, "run.queued", actorIn(ctx, id), map[string]any{"ticket": it.Key, "stage": stage, "adapter": r.Adapter})
	if err := rs.Store.CreateRun(ctx, r, e); err != nil {
		return RunView{}, err
	}
	if rs.Dispatcher != nil {
		rs.Dispatcher.Kick()
	}
	return RunView{Run: r, TicketKey: it.Key, ProjectKey: p.Key}, nil
}

// prepare applies the project's execution settings: the run starts in a
// workspace with the repository checked out on the ticket branch (see
// execution.Wrap), in the project's image, with its environment.
func (rs *Runs) prepare(ctx context.Context, r *run.Run, it tracker.Item) error {
	if rs.Execution == nil {
		return nil
	}
	x, err := rs.Execution.ExecutionSettings(ctx, r.ProjectID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	env := map[string]string{"BALLET_TICKET": it.Key, "BALLET_STAGE": r.Stage}
	for k, v := range x.Env {
		env[k] = v
	}
	if r.Spec.Image == "" {
		r.Spec.Image = x.Image
	}
	if x.RepoURL != "" {
		branch, err := execution.BranchName(x.BranchTemplate, it.Key, string(it.Type), it.Title)
		if err != nil {
			return invalid(err)
		}
		r.Branch = branch
		env["BALLET_BRANCH"] = branch
		r.Spec.Command = execution.Wrap(x, branch, true, r.Spec.Command)
	}
	for k, v := range r.Spec.Env {
		env[k] = v
	}
	r.Spec.Env = env
	return nil
}

// Get returns a run.
func (rs *Runs) Get(ctx context.Context, runID string) (RunView, error) {
	return rs.view(ctx, runID, ActTrackerRead)
}

// ListForTicket returns a ticket's runs, oldest first.
func (rs *Runs) ListForTicket(ctx context.Context, ticketKey string) ([]RunView, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	it, p, c, err := rs.ticket(ctx, ticketKey)
	if err != nil {
		return nil, err
	}
	if err := rs.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return nil, err
	}
	list, err := rs.Store.ListRuns(ctx, RunFilter{TicketID: it.ID})
	if err != nil {
		return nil, err
	}
	out := make([]RunView, 0, len(list))
	for _, r := range list {
		out = append(out, RunView{Run: r, TicketKey: it.Key, ProjectKey: p.Key})
	}
	return out, nil
}

// Logs returns up to limit chunks of a run's output after seq.
func (rs *Runs) Logs(ctx context.Context, runID string, afterSeq int64, limit int) ([]RunLog, error) {
	if _, err := rs.view(ctx, runID, ActTrackerRead); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	return rs.Store.RunLogs(ctx, runID, afterSeq, limit)
}

// Cancel stops a run: a queued run is cancelled at once; an active one is
// cancelled on its Runner (or here, when the Runner is gone).
func (rs *Runs) Cancel(ctx context.Context, runID string) (RunView, error) {
	v, err := rs.view(ctx, runID, ActRunManage)
	if err != nil {
		return RunView{}, err
	}
	if v.Status.Terminal() {
		return RunView{}, fmt.Errorf("%w: run already %s", ErrConflict, v.Status)
	}
	id, _ := caller(ctx)
	r, err := rs.Dispatcher.cancel(ctx, v.Run, actorIn(ctx, id))
	if err != nil {
		return RunView{}, err
	}
	v.Run = r
	return v, nil
}

func (rs *Runs) view(ctx context.Context, runID string, a Action) (RunView, error) {
	id, err := caller(ctx)
	if err != nil {
		return RunView{}, err
	}
	r, err := rs.Store.Run(ctx, runID)
	if err != nil {
		return RunView{}, err
	}
	it, err := rs.Items.ItemByID(ctx, r.TicketID)
	if err != nil {
		return RunView{}, err
	}
	p, err := rs.Tenancy.ProjectByID(ctx, r.ProjectID)
	if err != nil {
		return RunView{}, err
	}
	c, err := rs.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return RunView{}, err
	}
	if err := rs.Authz.Authorize(ctx, id, a, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return RunView{}, err
	}
	return RunView{Run: r, TicketKey: it.Key, ProjectKey: p.Key}, nil
}

func (rs *Runs) ticket(ctx context.Context, key string) (tracker.Item, tenancy.Project, tenancy.Customer, error) {
	it, err := rs.Items.ItemByKey(ctx, key)
	if err != nil {
		return tracker.Item{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	p, err := rs.Tenancy.ProjectByID(ctx, it.ProjectID)
	if err != nil {
		return tracker.Item{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	c, err := rs.Tenancy.CustomerByID(ctx, p.CustomerID)
	return it, p, c, err
}

func runEvent(r run.Run, customerID, typ string, actor event.Actor, payload map[string]any) event.Event {
	at := r.CreatedAt
	switch {
	case !r.FinishedAt.IsZero():
		at = r.FinishedAt
	case !r.StartedAt.IsZero():
		at = r.StartedAt
	}
	return event.Event{Customer: customerID, Project: r.ProjectID, EntityType: "run", EntityID: r.ID, Type: typ,
		Actor: actor, OccurredAt: at, Payload: mustJSON(payload)}
}

// RunnerConn reaches a connected Runner.
type RunnerConn interface {
	// Start sends a run with secretEnv: environment variables (tokens)
	// delivered with the start only, never stored with the run.
	Start(ctx context.Context, r run.Run, secretEnv map[string]string) error
	Cancel(ctx context.Context, runID string) error
}

// RunnerInfo describes a Runner as it introduced itself.
type RunnerInfo struct {
	Name     string
	Labels   map[string]string
	Capacity int
	Active   []string // runs it still executes (after a reconnect)
}

type runnerState struct {
	info   RunnerInfo
	conn   RunnerConn
	active map[string]bool
	ready  bool // reconciled: may receive runs
}

// Dispatcher assigns queued runs to connected Runners and records what
// they report. Runs held by a Runner that stays away longer than Grace
// fail.
type Dispatcher struct {
	Store       RunStore
	Tenancy     TenancyStore
	Now         func() time.Time
	Logger      *slog.Logger
	Grace       time.Duration // default 2 minutes
	MaxLogBytes int64         // per run; default 8 MiB
	Interval    time.Duration // dispatch and sweep period; default 1 second
	// SecretEnv returns the secrets a run receives with its start (the
	// project's git token); optional.
	SecretEnv func(ctx context.Context, r run.Run) (map[string]string, error)
	// Adapters read agent runs' results, by adapter name.
	Adapters map[string]agent.Adapter

	mu      sync.Mutex
	runners map[string]*runnerState
	gone    map[string]time.Time // disconnected Runners: since when
	kick    chan struct{}
}

// ErrRunnerConflict is returned when a Runner name is already connected.
var ErrRunnerConflict = errors.New("a runner with this name is already connected")

func (d *Dispatcher) init() {
	if d.runners == nil {
		d.runners = map[string]*runnerState{}
		d.gone = map[string]time.Time{}
		d.kick = make(chan struct{}, 1)
	}
}

// Kick asks the dispatcher to look at the queue now.
func (d *Dispatcher) Kick() {
	d.mu.Lock()
	d.init()
	d.mu.Unlock()
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run dispatches queued runs and fails orphaned ones until ctx ends.
func (d *Dispatcher) Run(ctx context.Context) {
	d.mu.Lock()
	d.init()
	kick := d.kick
	d.mu.Unlock()
	interval := d.Interval
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		d.dispatch(ctx)
		d.sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-t.C:
		}
	}
}

// Connect registers a Runner. Runs assigned to it that it no longer
// executes fail; runs it still executes stay active.
func (d *Dispatcher) Connect(ctx context.Context, info RunnerInfo, conn RunnerConn) error {
	if info.Name == "" || info.Capacity < 1 {
		return fmt.Errorf("%w: a runner needs a name and a capacity of at least 1", ErrInvalid)
	}
	d.mu.Lock()
	d.init()
	if _, taken := d.runners[info.Name]; taken {
		d.mu.Unlock()
		return ErrRunnerConflict
	}
	// Not ready until reconciled: a run assigned to this connection in the
	// meantime would look like one the Runner lost.
	st := &runnerState{info: info, conn: conn, active: map[string]bool{}}
	d.runners[info.Name] = st
	delete(d.gone, info.Name)
	d.mu.Unlock()

	still := map[string]bool{}
	for _, id := range info.Active {
		still[id] = true
	}
	held, err := d.Store.ListRuns(ctx, RunFilter{Runner: info.Name, Statuses: []run.Status{run.StatusStarting, run.StatusRunning}})
	if err != nil {
		d.mu.Lock()
		delete(d.runners, info.Name)
		d.mu.Unlock()
		return err
	}
	for _, r := range held {
		if still[r.ID] {
			d.mu.Lock()
			st.active[r.ID] = true
			d.mu.Unlock()
			continue
		}
		d.finish(ctx, r, run.StatusFailed, nil, "the runner restarted and no longer executes this run")
	}
	d.mu.Lock()
	st.ready = true
	d.mu.Unlock()
	d.Kick()
	return nil
}

// Disconnect unregisters a Runner connection. Its runs stay active for
// Grace in case it reconnects.
func (d *Dispatcher) Disconnect(name string, conn RunnerConn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.init()
	if st, ok := d.runners[name]; ok && st.conn == conn {
		delete(d.runners, name)
		d.gone[name] = d.Now()
	}
}

// Runners returns the connected Runners.
func (d *Dispatcher) Runners() []RunnerInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]RunnerInfo, 0, len(d.runners))
	for _, st := range d.runners {
		info := st.info
		info.Active = nil
		for id := range st.active {
			info.Active = append(info.Active, id)
		}
		out = append(out, info)
	}
	return out
}

// dispatch assigns queued runs, oldest first, to Runners with free capacity.
func (d *Dispatcher) dispatch(ctx context.Context) {
	queued, err := d.Store.ListRuns(ctx, RunFilter{Statuses: []run.Status{run.StatusQueued}, Limit: 100})
	if err != nil {
		d.logger().ErrorContext(ctx, "list queued runs failed", "error", err)
		return
	}
	for _, r := range queued {
		st := d.pick()
		if st == nil {
			return
		}
		assigned := r
		assigned.Status, assigned.Runner, assigned.Version = run.StatusStarting, st.info.Name, r.Version+1
		if err := d.Store.UpdateRun(ctx, assigned, r.Version, nil); err != nil {
			if !errors.Is(err, ErrConflict) {
				d.logger().ErrorContext(ctx, "assign run failed", "run", r.ID, "error", err)
			}
			continue
		}
		d.mu.Lock()
		st.active[r.ID] = true
		d.mu.Unlock()
		var secrets map[string]string
		if d.SecretEnv != nil {
			if secrets, err = d.SecretEnv(ctx, assigned); err != nil {
				d.mu.Lock()
				delete(st.active, r.ID)
				d.mu.Unlock()
				d.finish(ctx, assigned, run.StatusFailed, nil, "could not resolve the run's credentials")
				d.logger().ErrorContext(ctx, "resolve run secrets failed", "run", r.ID, "error", err)
				continue
			}
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := st.conn.Start(callCtx, assigned, secrets)
		cancel()
		if err != nil {
			d.logger().WarnContext(ctx, "runner refused run; requeued", "run", r.ID, "runner", st.info.Name, "error", err)
			d.mu.Lock()
			delete(st.active, r.ID)
			d.mu.Unlock()
			back := assigned
			back.Status, back.Runner, back.Version = run.StatusQueued, "", assigned.Version+1
			_ = d.Store.UpdateRun(ctx, back, assigned.Version, nil)
		}
	}
}

// pick returns a connected Runner with free capacity (the least loaded).
func (d *Dispatcher) pick() *runnerState {
	d.mu.Lock()
	defer d.mu.Unlock()
	var best *runnerState
	for _, st := range d.runners {
		if !st.ready {
			continue
		}
		free := st.info.Capacity - len(st.active)
		if free > 0 && (best == nil || free > best.info.Capacity-len(best.active)) {
			best = st
		}
	}
	return best
}

// sweep fails active runs whose Runner has been gone longer than Grace.
func (d *Dispatcher) sweep(ctx context.Context) {
	grace := d.Grace
	if grace <= 0 {
		grace = 2 * time.Minute
	}
	active, err := d.Store.ListRuns(ctx, RunFilter{Statuses: []run.Status{run.StatusStarting, run.StatusRunning}})
	if err != nil {
		return
	}
	now := d.Now()
	for _, r := range active {
		d.mu.Lock()
		_, connected := d.runners[r.Runner]
		since, known := d.gone[r.Runner]
		d.mu.Unlock()
		if connected {
			continue
		}
		if !known {
			// Core restarted: the Runner gets Grace from now to reconnect.
			d.mu.Lock()
			d.gone[r.Runner] = now
			d.mu.Unlock()
			continue
		}
		if now.Sub(since) > grace {
			d.finish(ctx, r, run.StatusFailed, nil, "the runner disconnected")
		}
	}
}

// Running records that a Runner started a run's session.
func (d *Dispatcher) Running(ctx context.Context, runner, runID string) error {
	r, err := d.held(ctx, runner, runID)
	if err != nil {
		return err
	}
	if r.Status != run.StatusStarting {
		return nil
	}
	next := r
	next.Status, next.StartedAt, next.Version = run.StatusRunning, d.Now(), r.Version+1
	e, err := d.event(ctx, next, "run.started", map[string]any{"runner": runner})
	if err != nil {
		return err
	}
	return d.Store.UpdateRun(ctx, next, r.Version, &e)
}

// Log records output of a run. Output beyond MaxLogBytes is dropped.
func (d *Dispatcher) Log(ctx context.Context, runner, runID, stream, text string) error {
	if stream != "stdout" && stream != "stderr" && stream != "system" {
		return fmt.Errorf("%w: stream must be stdout, stderr or system", ErrInvalid)
	}
	if _, err := d.held(ctx, runner, runID); err != nil {
		return err
	}
	max := d.MaxLogBytes
	if max <= 0 {
		max = 8 << 20
	}
	_, err := d.Store.AppendRunLog(ctx, runID, stream, text, d.Now(), max)
	return err
}

// Finished records the end of a run reported by its Runner.
func (d *Dispatcher) Finished(ctx context.Context, runner, runID string, exitCode int, errText string, cancelled bool) error {
	r, err := d.held(ctx, runner, runID)
	if err != nil {
		return err
	}
	if a, ok := d.Adapters[r.Adapter]; ok && r.Adapter != "" && !cancelled {
		stdout, err := d.stdout(ctx, r.ID)
		if err != nil {
			return err
		}
		res, ok := a.Result(stdout)
		switch {
		case ok:
			r.Result = &run.Result{Summary: truncate(res.Summary, 10_000), Turns: res.Turns, CostUSD: res.CostUSD}
			if !res.Success && exitCode == 0 && errText == "" {
				errText = "the agent reported a failure: " + truncate(res.Summary, 500)
			}
		case exitCode == 0 && errText == "":
			errText = "the agent reported no result"
		}
	}
	switch {
	case cancelled:
		return d.finish(ctx, r, run.StatusCancelled, nil, firstNonEmpty(errText, "cancelled"))
	case errText != "":
		return d.finish(ctx, r, run.StatusFailed, nil, errText)
	case exitCode != 0:
		return d.finish(ctx, r, run.StatusFailed, &exitCode, fmt.Sprintf("exited with code %d", exitCode))
	}
	return d.finish(ctx, r, run.StatusSucceeded, &exitCode, "")
}

// stdout returns a run's standard output.
func (d *Dispatcher) stdout(ctx context.Context, runID string) (string, error) {
	var b strings.Builder
	var after int64
	for {
		logs, err := d.Store.RunLogs(ctx, runID, after, 1000)
		if err != nil {
			return "", err
		}
		for _, l := range logs {
			if l.Stream == "stdout" {
				b.WriteString(l.Text)
			}
			after = l.Seq
		}
		if len(logs) < 1000 {
			return b.String(), nil
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// cancel cancels a run on behalf of a human.
func (d *Dispatcher) cancel(ctx context.Context, r run.Run, actor event.Actor) (run.Run, error) {
	if r.Status == run.StatusQueued {
		next := r
		next.Status, next.FinishedAt, next.Error, next.Version = run.StatusCancelled, d.Now(), "cancelled", r.Version+1
		e, err := d.event(ctx, next, "run.finished", map[string]any{"status": next.Status})
		if err != nil {
			return run.Run{}, err
		}
		e.Actor = actor
		return next, d.Store.UpdateRun(ctx, next, r.Version, &e)
	}
	d.mu.Lock()
	d.init()
	st, connected := d.runners[r.Runner]
	d.mu.Unlock()
	if connected {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := st.conn.Cancel(callCtx, r.ID); err == nil {
			return r, nil // the Runner reports run.finished
		}
	}
	return d.finishRun(ctx, r, run.StatusCancelled, nil, "cancelled")
}

func (d *Dispatcher) finish(ctx context.Context, r run.Run, status run.Status, exitCode *int, errText string) error {
	_, err := d.finishRun(ctx, r, status, exitCode, errText)
	if err != nil {
		d.logger().ErrorContext(ctx, "finish run failed", "run", r.ID, "error", err)
	}
	return err
}

func (d *Dispatcher) finishRun(ctx context.Context, r run.Run, status run.Status, exitCode *int, errText string) (run.Run, error) {
	next := r
	next.Status, next.ExitCode, next.Error, next.FinishedAt, next.Version = status, exitCode, errText, d.Now(), r.Version+1
	payload := map[string]any{"status": status}
	if exitCode != nil {
		payload["exit_code"] = *exitCode
	}
	e, err := d.event(ctx, next, "run.finished", payload)
	if err != nil {
		return run.Run{}, err
	}
	if err := d.Store.UpdateRun(ctx, next, r.Version, &e); err != nil {
		return run.Run{}, err
	}
	d.mu.Lock()
	if st, ok := d.runners[r.Runner]; ok {
		delete(st.active, r.ID)
	}
	d.mu.Unlock()
	d.Kick()
	return next, nil
}

// held returns a run the Runner executes.
func (d *Dispatcher) held(ctx context.Context, runner, runID string) (run.Run, error) {
	r, err := d.Store.Run(ctx, runID)
	if err != nil {
		return run.Run{}, err
	}
	if r.Runner != runner || !r.Status.Active() {
		return run.Run{}, fmt.Errorf("%w: run %s is not executed by runner %s", ErrForbidden, runID, runner)
	}
	return r, nil
}

func (d *Dispatcher) event(ctx context.Context, r run.Run, typ string, payload map[string]any) (event.Event, error) {
	p, err := d.Tenancy.ProjectByID(ctx, r.ProjectID)
	if err != nil {
		return event.Event{}, err
	}
	return runEvent(r, p.CustomerID, typ, event.Actor{Kind: event.ActorService, Subject: "runner:" + r.Runner}, payload), nil
}

func (d *Dispatcher) logger() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
