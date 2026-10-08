package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/agent"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
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
	// SaveRunState and RunState keep what continues a parked session.
	SaveRunState(ctx context.Context, runID string, data []byte) error
	RunState(ctx context.Context, runID string) ([]byte, error)
}

// RunFilter selects runs; zero fields do not filter.
type RunFilter struct {
	TicketID string
	Agent    string
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
// ticket and executed by agents through the Dispatcher.
type Runs struct {
	Store     RunStore
	Execution ExecutionStore // nil: runs execute their spec as given
	// LLMURL is the LLM gateway as reached from sessions; TokenEnv names
	// the secret variable holding the run token.
	LLMURL   string
	TokenEnv string
	// SessionSkills returns the skills a project's sessions get.
	SessionSkills func(ctx context.Context, projectKey string) ([]agent.Skill, error)
	MCP           []agent.MCPServer // MCP servers every session gets
	Model         string            // agent model; "": the runtime's default
	// Deps and Knowledge feed the onboarding bundle; both optional.
	Deps       DependencyStore
	Knowledge  func(ctx context.Context, customer, project, ticket, query string, limit int) ([]onboarding.Knowledge, error)
	Items      ItemStore
	Tenancy    TenancyStore
	Authz      Authorizer
	Dispatcher *Dispatcher
	// Questions lets parked sessions resume with their answers (ADR-0026);
	// nil: a stage always starts a new session.
	Questions QuestionLister
	// AnswerWindow is how long sessions wait for answers before they park
	// when the project sets none; 0: DefaultAnswerWindow.
	AnswerWindow time.Duration
	Logger       *slog.Logger
	Now          func() time.Time
	NewID        func() string
}

func (rs *Runs) logger() *slog.Logger {
	if rs.Logger != nil {
		return rs.Logger
	}
	return slog.Default()
}

// AgentInput describes an agent run.
type AgentInput struct {
	Adapter        string // the runtime, e.g. "claude-code"
	Prompt         string
	TimeoutSeconds int
}

// CreateAgent queues a run executing a coding-agent session on a runtime:
// the session gets the prompt, the project's skills and the MCP servers.
// Like Create, it needs run.manage.
func (rs *Runs) CreateAgent(ctx context.Context, ticketKey, stage string, in AgentInput) (RunView, error) {
	if !slices.Contains(agent.Runtimes, in.Adapter) {
		return RunView{}, fmt.Errorf("%w: unknown agent runtime %q", ErrInvalid, in.Adapter)
	}
	if len(in.Prompt) > 20_000 {
		return RunView{}, fmt.Errorf("%w: the prompt must be at most 20000 characters", ErrInvalid)
	}
	return rs.create(ctx, ticketKey, stage, func(it tracker.Item, p tenancy.Project) (run.Spec, string, error) {
		return rs.agentSpec(ctx, it, p, stage, in, StageOptions{})
	})
}

// StageOptions shape an agent run of a pipeline stage.
type StageOptions struct {
	Instructions string   // the stage's instructions; "": Ballet's default for the stage
	Skills       []string // project skills to include; empty: all
	Model        string   // "": Runs.Model
	Artifacts    string   // what earlier stages handed over (their reports)
}

// agentSpec builds an agent run's spec: a session with the onboarding
// bundle as prompt, the project's skills and the MCP servers.
func (rs *Runs) agentSpec(ctx context.Context, it tracker.Item, p tenancy.Project, stage string,
	in AgentInput, opts StageOptions) (run.Spec, string, error) {
	extra := strings.TrimSpace(strings.Join([]string{opts.Artifacts, in.Prompt}, "\n\n"))
	bundle, err := rs.Bundle(ctx, it, p, stage, extra)
	if err != nil {
		return run.Spec{}, "", err
	}
	if opts.Instructions != "" {
		bundle.StageInstructions = opts.Instructions
	}
	var skills []agent.Skill
	if rs.SessionSkills != nil {
		all, err := rs.SessionSkills(ctx, p.Key)
		if err != nil {
			return run.Spec{}, "", err
		}
		for _, sk := range all {
			if len(opts.Skills) == 0 || slices.Contains(opts.Skills, sk.Name) {
				skills = append(skills, sk)
			}
		}
	}
	model := rs.Model
	if opts.Model != "" {
		model = opts.Model
	}
	sess := &agent.Session{
		Runtime: in.Adapter, Prompt: bundle.Render(onboarding.DefaultMaxBytes), Instructions: sessionInstructions,
		Skills: skills, MCP: rs.MCP, Model: model, LLMURL: rs.LLMURL, TokenEnv: rs.TokenEnv,
	}
	if resume, answers, ok := rs.resumable(ctx, it.ID, stage); ok {
		// The parked session continues with its context; its next message
		// is the answers.
		sess.Resume, sess.Prompt = resume, answers
	}
	return run.Spec{TimeoutSeconds: in.TimeoutSeconds, Session: sess}, in.Adapter, nil
}

// CreateForStage queues the agent run of a pipeline stage for Core's
// orchestrator: no caller, no authorization.
func (rs *Runs) CreateForStage(ctx context.Context, it tracker.Item, stage, adapter string, opts StageOptions, timeoutSeconds int) (RunView, error) {
	if !slices.Contains(agent.Runtimes, adapter) {
		return RunView{}, fmt.Errorf("%w: unknown agent runtime %q", ErrPermanent, adapter)
	}
	p, err := rs.Tenancy.ProjectByID(ctx, it.ProjectID)
	if err != nil {
		return RunView{}, err
	}
	c, err := rs.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return RunView{}, err
	}
	return rs.insert(ctx, it, p, c, stage, func(it tracker.Item, p tenancy.Project) (run.Spec, string, error) {
		return rs.agentSpec(ctx, it, p, stage, AgentInput{Adapter: adapter, TimeoutSeconds: timeoutSeconds}, opts)
	}, event.System, "ballet")
}

// sessionInstructions are the standing instructions of every agent session.
const sessionInstructions = `You are a coding agent run by Ballet. You work unattended; humans may send
you messages, and answers to your blocking questions arrive as messages.
Your task, the ticket and its context are in the prompt.
Use the knowledge tools to look up and record what the project knows.
Work in the current repository on the current branch, commit and push.
End with a short summary of what you did and what is left.`

// Bundle gathers a run's onboarding bundle (best effort: missing context
// does not stop the run).
func (rs *Runs) Bundle(ctx context.Context, it tracker.Item, p tenancy.Project, stage, extra string) (onboarding.Bundle, error) {
	b := onboarding.Bundle{
		Project: p.Key, Stage: stage, StageInstructions: onboarding.DefaultStageInstructions(stage),
		Ticket: onboarding.Item{Key: it.Key, Kind: string(it.Kind), Title: it.Title, State: string(it.State), Description: it.Description},
		Type:   string(it.Type), AcceptanceCriteria: it.AcceptanceCriteria,
		ReviewMode: string(it.Policy.ReviewMode), MergeMode: string(it.Policy.MergeMode), Extra: extra,
	}
	item := func(id string) *onboarding.Item {
		if id == "" {
			return nil
		}
		x, err := rs.Items.ItemByID(ctx, id)
		if err != nil {
			return nil
		}
		return &onboarding.Item{Key: x.Key, Kind: string(x.Kind), Title: x.Title, State: string(x.State), Description: x.Description}
	}
	b.Epic, b.Milestone = item(it.EpicID), item(it.MilestoneID)
	if rs.Execution != nil {
		if x, err := rs.Execution.ExecutionSettings(ctx, p.ID); err == nil && x.RepoURL != "" {
			b.Branch, _ = execution.BranchName(x.BranchTemplate, it.Key, string(it.Type), it.Title)
		}
	}
	if rs.Deps != nil {
		deps, err := rs.Deps.ItemDependencies(ctx, it.ID)
		if err != nil {
			return onboarding.Bundle{}, err
		}
		for _, d := range deps {
			otherID, relation := d.ToID, "blocks"
			switch {
			case d.Type == tracker.DepRelates:
				relation = "relates"
				if d.ToID == it.ID {
					otherID = d.FromID
				}
			case d.ToID == it.ID:
				otherID, relation = d.FromID, "blocked_by"
			}
			o := item(otherID)
			if o == nil {
				continue
			}
			b.Dependencies = append(b.Dependencies, onboarding.Dependency{Item: *o, Relation: relation, Report: rs.latestReport(ctx, otherID)})
		}
	}
	if rs.Knowledge != nil {
		c, err := rs.Tenancy.CustomerByID(ctx, p.CustomerID)
		if err != nil {
			return onboarding.Bundle{}, err
		}
		if k, err := rs.Knowledge(ctx, c.Key, p.Key, it.Key, it.Title, 5); err == nil {
			b.Knowledge = k
		}
	}
	return b, nil
}

// latestReport is the summary of the latest finished agent run of an item.
func (rs *Runs) latestReport(ctx context.Context, itemID string) string {
	runs, err := rs.Store.ListRuns(ctx, RunFilter{TicketID: itemID, Statuses: []run.Status{run.StatusSucceeded, run.StatusFailed}})
	if err != nil {
		return ""
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Result != nil && runs[i].Result.Summary != "" {
			return fmt.Sprintf("%s run (%s): %s", runs[i].Stage, runs[i].Status, runs[i].Result.Summary)
		}
	}
	return ""
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
	return rs.insert(ctx, it, p, c, stage, build, actorIn(ctx, id), id.Subject)
}

func (rs *Runs) insert(ctx context.Context, it tracker.Item, p tenancy.Project, c tenancy.Customer, stage string,
	build func(tracker.Item, tenancy.Project) (run.Spec, string, error), actor event.Actor, createdBy string) (RunView, error) {
	spec, adapter, err := build(it, p)
	if err != nil {
		return RunView{}, err
	}
	r := run.Run{ID: rs.NewID(), ProjectID: p.ID, TicketID: it.ID, Stage: stage, Status: run.StatusQueued, Spec: spec,
		Adapter: adapter, CreatedBy: createdBy, CreatedAt: rs.Now(), Version: 1}
	if err := r.Validate(); err != nil {
		return RunView{}, invalid(err)
	}
	if err := rs.prepare(ctx, &r, it); err != nil {
		return RunView{}, err
	}
	e := runEvent(r, c.ID, "run.queued", actor, map[string]any{"ticket": it.Key, "stage": stage, "adapter": r.Adapter})
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
	if r.Spec.Pool == "" {
		r.Spec.Pool = x.Pool
	}
	if x.RepoURL != "" {
		branch, err := execution.BranchName(x.BranchTemplate, it.Key, string(it.Type), it.Title)
		if err != nil {
			return invalid(err)
		}
		r.Branch = branch
		env["BALLET_BRANCH"] = branch
		if r.Spec.Session != nil {
			// The workspace is prepared first; the session then works in
			// the repository.
			r.Spec.Command = execution.Wrap(x, branch, true, []string{"true"})
			r.Spec.Session.Dir = execution.RepoDir
		} else {
			r.Spec.Command = execution.Wrap(x, branch, true, r.Spec.Command)
		}
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
// cancelled on its agent (or here, when the agent is gone).
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

// Input kinds a human sends to a running session.
const (
	InputMessage   = "message"   // delivered when the current turn ends
	InputInterrupt = "interrupt" // stops the current turn, then delivers the text, if any
)

// Input sends a human's message or interrupt to a run's running
// coding-agent session (ADR-0026). It needs run.manage.
func (rs *Runs) Input(ctx context.Context, runID, kind, text string) error {
	v, err := rs.view(ctx, runID, ActRunManage)
	if err != nil {
		return err
	}
	switch {
	case kind != InputMessage && kind != InputInterrupt:
		return fmt.Errorf("%w: kind must be message or interrupt", ErrInvalid)
	case kind == InputMessage && strings.TrimSpace(text) == "":
		return fmt.Errorf("%w: a message needs text", ErrInvalid)
	case len(text) > 20_000:
		return fmt.Errorf("%w: the text must be at most 20000 characters", ErrInvalid)
	case v.Spec.Session == nil:
		return fmt.Errorf("%w: the run is not a coding-agent session", ErrInvalid)
	case v.Status != run.StatusRunning:
		return fmt.Errorf("%w: the run is %s", ErrConflict, v.Status)
	}
	return rs.Dispatcher.input(ctx, v.Run, kind, text)
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

// AgentConn reaches a connected agent.
type AgentConn interface {
	// Start sends a run with secretEnv: environment variables (tokens)
	// delivered with the start only, never stored with the run.
	Start(ctx context.Context, r run.Run, secretEnv map[string]string) error
	Cancel(ctx context.Context, runID string) error
	// Input delivers a human's input to the run's running session.
	Input(ctx context.Context, runID, kind, text string) error
}

// AgentInfo describes an agent as it introduced itself.
type AgentInfo struct {
	Name     string
	Labels   map[string]string
	Capacity int
	Active   []string // runs it still executes (after a reconnect)
}

type agentState struct {
	info   AgentInfo
	conn   AgentConn
	active map[string]bool
	ready  bool // reconciled: may receive runs
}

// Dispatcher assigns queued runs to connected agents and records what
// they report. Runs held by an agent that stays away longer than Grace
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
	// OnFinished is called after a run ended (the pipeline continues);
	// optional.
	OnFinished func(ctx context.Context, r run.Run)
	// Held, when set, keeps queued runs from starting (a paused project:
	// Control.Paused).
	Held func(ctx context.Context, projectID string) bool

	mu     sync.Mutex
	agents map[string]*agentState
	gone   map[string]time.Time // disconnected agents: since when
	kick   chan struct{}
}

// ErrAgentConflict is returned when an agent name is already connected.
var ErrAgentConflict = errors.New("an agent with this name is already connected")

func (d *Dispatcher) init() {
	if d.agents == nil {
		d.agents = map[string]*agentState{}
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

// Connect registers an agent. Runs assigned to it that it no longer
// executes fail; runs it still executes stay active.
func (d *Dispatcher) Connect(ctx context.Context, info AgentInfo, conn AgentConn) error {
	if info.Name == "" || info.Capacity < 1 {
		return fmt.Errorf("%w: an agent needs a name and a capacity of at least 1", ErrInvalid)
	}
	d.mu.Lock()
	d.init()
	if _, taken := d.agents[info.Name]; taken {
		d.mu.Unlock()
		return ErrAgentConflict
	}
	// Not ready until reconciled: a run assigned to this connection in the
	// meantime would look like one the agent lost.
	st := &agentState{info: info, conn: conn, active: map[string]bool{}}
	d.agents[info.Name] = st
	delete(d.gone, info.Name)
	d.mu.Unlock()

	still := map[string]bool{}
	for _, id := range info.Active {
		still[id] = true
	}
	held, err := d.Store.ListRuns(ctx, RunFilter{Agent: info.Name, Statuses: []run.Status{run.StatusStarting, run.StatusRunning}})
	if err != nil {
		d.mu.Lock()
		delete(d.agents, info.Name)
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
		d.finish(ctx, r, run.StatusFailed, nil, "the agent restarted and no longer executes this run")
	}
	d.mu.Lock()
	st.ready = true
	d.mu.Unlock()
	d.Kick()
	return nil
}

// Disconnect unregisters an agent connection. Its runs stay active for
// Grace in case it reconnects.
func (d *Dispatcher) Disconnect(name string, conn AgentConn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.init()
	if st, ok := d.agents[name]; ok && st.conn == conn {
		delete(d.agents, name)
		d.gone[name] = d.Now()
	}
}

// Agents returns the connected agents.
func (d *Dispatcher) Agents() []AgentInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]AgentInfo, 0, len(d.agents))
	for _, st := range d.agents {
		info := st.info
		info.Active = nil
		for id := range st.active {
			info.Active = append(info.Active, id)
		}
		out = append(out, info)
	}
	return out
}

// dispatch assigns queued runs, oldest first, to agents with free capacity.
func (d *Dispatcher) dispatch(ctx context.Context) {
	queued, err := d.Store.ListRuns(ctx, RunFilter{Statuses: []run.Status{run.StatusQueued}, Limit: 100})
	if err != nil {
		d.logger().ErrorContext(ctx, "list queued runs failed", "error", err)
		return
	}
	for _, r := range queued {
		if d.Held != nil && d.Held(ctx, r.ProjectID) {
			continue
		}
		st := d.pick(r.Spec.Pool)
		if st == nil {
			continue // no free agent of its pool; others may have one
		}
		assigned := r
		assigned.Status, assigned.Agent, assigned.Version = run.StatusStarting, st.info.Name, r.Version+1
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
		if rs := assigned.Spec.Session; rs != nil && rs.Resume != nil {
			resume := *rs.Resume
			if resume.State, err = d.Store.RunState(ctx, resume.RunID); err != nil {
				d.logger().WarnContext(ctx, "parked session state unavailable", "run", r.ID, "error", err)
			}
			sess := *rs
			sess.Resume = &resume
			assigned.Spec.Session = &sess
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := st.conn.Start(callCtx, assigned, secrets)
		cancel()
		if err != nil {
			d.logger().WarnContext(ctx, "agent refused run; requeued", "run", r.ID, "agent", st.info.Name, "error", err)
			d.mu.Lock()
			delete(st.active, r.ID)
			d.mu.Unlock()
			back := assigned
			back.Status, back.Agent, back.Version = run.StatusQueued, "", assigned.Version+1
			_ = d.Store.UpdateRun(ctx, back, assigned.Version, nil)
		}
	}
}

// pick returns a connected agent with free capacity (the least loaded),
// of the pool when one is given.
func (d *Dispatcher) pick(pool string) *agentState {
	d.mu.Lock()
	defer d.mu.Unlock()
	var best *agentState
	for _, st := range d.agents {
		if !st.ready || pool != "" && st.info.Labels["pool"] != pool {
			continue
		}
		free := st.info.Capacity - len(st.active)
		if free > 0 && (best == nil || free > best.info.Capacity-len(best.active)) {
			best = st
		}
	}
	return best
}

// sweep fails active runs whose agent has been gone longer than Grace.
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
		_, connected := d.agents[r.Agent]
		since, known := d.gone[r.Agent]
		d.mu.Unlock()
		if connected {
			continue
		}
		if !known {
			// Core restarted: the agent gets Grace from now to reconnect.
			d.mu.Lock()
			d.gone[r.Agent] = now
			d.mu.Unlock()
			continue
		}
		if now.Sub(since) > grace {
			d.finish(ctx, r, run.StatusFailed, nil, "the agent disconnected")
		}
	}
}

// Running records that an agent started a run's session.
func (d *Dispatcher) Running(ctx context.Context, agentName, runID string) error {
	r, err := d.held(ctx, agentName, runID)
	if err != nil {
		return err
	}
	if r.Status != run.StatusStarting {
		return nil
	}
	next := r
	next.Status, next.StartedAt, next.Version = run.StatusRunning, d.Now(), r.Version+1
	e, err := d.event(ctx, next, "run.started", map[string]any{"agent": agentName})
	if err != nil {
		return err
	}
	return d.Store.UpdateRun(ctx, next, r.Version, &e)
}

// Log records output of a run: stdout, stderr, the agent's system
// messages, and a session's normalized events (stream "event", one JSON
// object per line). Output beyond MaxLogBytes is dropped.
func (d *Dispatcher) Log(ctx context.Context, agentName, runID, stream, text string) error {
	if stream != "stdout" && stream != "stderr" && stream != "system" && stream != "event" {
		return fmt.Errorf("%w: stream must be stdout, stderr, system or event", ErrInvalid)
	}
	if _, err := d.held(ctx, agentName, runID); err != nil {
		return err
	}
	max := d.MaxLogBytes
	if max <= 0 {
		max = 8 << 20
	}
	_, err := d.Store.AppendRunLog(ctx, runID, stream, text, d.Now(), max)
	return err
}

// SessionResult is what a coding-agent session reported at its end.
type SessionResult struct {
	Success bool
	Summary string
	Turns   int
	CostUSD float64
	// Parked: the session ended to wait for answers; SessionID and State
	// (nil when the agent could not save it) continue it.
	Parked    bool
	SessionID string
	State     []byte
}

// Finished records the end of a run reported by its agent; res is the
// session's result (nil when it reported none).
func (d *Dispatcher) Finished(ctx context.Context, agentName, runID string, exitCode int, errText string, cancelled bool,
	res *SessionResult) error {
	r, err := d.held(ctx, agentName, runID)
	if err != nil {
		return err
	}
	if r.Spec.Session != nil && !cancelled {
		switch {
		case res != nil:
			r.Result = &run.Result{Summary: truncate(res.Summary, 10_000), Turns: res.Turns, CostUSD: res.CostUSD,
				Parked: res.Parked, SessionID: res.SessionID}
			if res.Parked && len(res.State) > 0 {
				if err := d.Store.SaveRunState(ctx, r.ID, res.State); err != nil {
					d.logger().ErrorContext(ctx, "save parked session state failed", "run", r.ID, "error", err)
					r.Result.SessionID = "" // cannot resume: the next session starts afresh
				}
			} else if res.Parked {
				r.Result.SessionID = ""
			}
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
	st, connected := d.agents[r.Agent]
	d.mu.Unlock()
	if connected {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := st.conn.Cancel(callCtx, r.ID); err == nil {
			return r, nil // the agent reports run.finished
		}
	}
	return d.finishRun(ctx, r, run.StatusCancelled, nil, "cancelled")
}

// input forwards a human's input to the agent executing the run.
func (d *Dispatcher) input(ctx context.Context, r run.Run, kind, text string) error {
	d.mu.Lock()
	d.init()
	st, connected := d.agents[r.Agent]
	d.mu.Unlock()
	if !connected {
		return fmt.Errorf("%w: the run's agent is not connected", ErrConflict)
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := st.conn.Input(callCtx, r.ID, kind, text); err != nil {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return nil
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
	if st, ok := d.agents[r.Agent]; ok {
		delete(st.active, r.ID)
	}
	d.mu.Unlock()
	d.Kick()
	if d.OnFinished != nil {
		d.OnFinished(ctx, next)
	}
	return next, nil
}

// held returns a run the agent executes.
func (d *Dispatcher) held(ctx context.Context, agentName, runID string) (run.Run, error) {
	r, err := d.Store.Run(ctx, runID)
	if err != nil {
		return run.Run{}, err
	}
	if r.Agent != agentName || !r.Status.Active() {
		return run.Run{}, fmt.Errorf("%w: run %s is not executed by agent %s", ErrForbidden, runID, agentName)
	}
	return r, nil
}

func (d *Dispatcher) event(ctx context.Context, r run.Run, typ string, payload map[string]any) (event.Event, error) {
	p, err := d.Tenancy.ProjectByID(ctx, r.ProjectID)
	if err != nil {
		return event.Event{}, err
	}
	return runEvent(r, p.CustomerID, typ, event.Actor{Kind: event.ActorService, Subject: "agent:" + r.Agent}, payload), nil
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
