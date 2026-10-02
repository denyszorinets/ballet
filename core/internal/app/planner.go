package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/kit/auth"
)

// PlannerStore persists planner sessions and transcripts.
type PlannerStore interface {
	CreatePlannerSession(ctx context.Context, s planner.Session, e event.Event) error
	PlannerSession(ctx context.Context, id string) (planner.Session, error)
	ListPlannerSessions(ctx context.Context, projectID string) ([]planner.Session, error)
	AppendPlannerMessage(ctx context.Context, m planner.Message, e event.Event) (planner.Message, error)
	PlannerMessages(ctx context.Context, sessionID string) ([]planner.Message, error)
	SetPlannerSummary(ctx context.Context, sessionID, summary string, upTo int64, e event.Event) error
	// QuestionSession returns a question's sub-chat (ErrNotFound if none).
	QuestionSession(ctx context.Context, questionID string) (planner.Session, error)
}

// LLM streams one model response (the Anthropic Messages API through the
// LLM gateway in production).
type LLM interface {
	Stream(ctx context.Context, req LLMRequest, onText func(string)) (LLMResponse, error)
}

// LLMRequest is one call of the model.
type LLMRequest struct {
	Caller    LLMCaller // whom the call is authorized and metered for
	Model     string
	System    string
	Messages  []planner.Message
	Tools     []ToolSpec
	MaxTokens int
}

// LLMCaller identifies a planner session to the LLM gateway.
type LLMCaller struct {
	CustomerKey string
	ProjectKey  string
	SessionID   string
	ActingFor   string // subject of the human
	TicketKey   string // the ticket the call is for (metering), if any
}

// LLMResponse is a complete model response.
type LLMResponse struct {
	Content    []planner.Block
	StopReason string // "end_turn", "tool_use", "max_tokens", ...
	Usage      planner.Usage
}

// ToolSpec describes a tool to the model.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema of the input object
}

// PlannerTool is something the planner can do. Call runs with the context
// of the turn, which carries the human's identity: tools are authorized as
// the human the planner acts for.
type PlannerTool interface {
	Spec() ToolSpec
	Call(ctx context.Context, env ToolEnv, input json.RawMessage) (string, error)
}

// ToolEnv is where a tool call happens.
type ToolEnv struct {
	CustomerKey string
	ProjectKey  string
	SessionID   string
}

// Output types sent to watchers of a session.
const (
	OutputText       = "text"        // streamed assistant text
	OutputToolCall   = "tool_call"   // the model called a tool
	OutputToolResult = "tool_result" // the tool returned
	OutputMessage    = "message"     // a message was stored (reload the transcript)
	OutputCompacted  = "compacted"   // older messages were summarized for the model
	OutputDone       = "done"        // the turn ended; Text is the reason
	OutputError      = "error"       // the turn failed; Text is the error
)

// PlannerOutput is live output of a session's running turn.
type PlannerOutput struct {
	Session   string
	Type      string
	Text      string
	Tool      string
	ToolUseID string
	Input     json.RawMessage
	IsError   bool
	Seq       int64 // OutputMessage
}

// DefaultPlannerInstructions is the planner's system prompt; a project's
// "planner" skill is appended to it.
const DefaultPlannerInstructions = `You are the planner of a software project in Ballet, an orchestrator of
AI software development. You work with a human engineer in this chat: you
research, write knowledge, and turn intent into milestones, epics and
tickets with dependencies and acceptance criteria.

You never change the plan directly. Propose changes as a plan changeset;
the human approves it, wholly or in part. Keep tickets small, testable and
independently understandable. Ask the human when requirements are unclear.`

// Planner runs planner sessions (ADR-0020): one conversation loop per
// session inside Core, calling the LLM with tools. Each turn runs in the
// background; watchers receive its live output, and every message is
// persisted, so a session survives a restart of Core.
type Planner struct {
	Store   PlannerStore
	Tenancy TenancyStore
	Authz   Authorizer
	LLM     LLM
	Tools   []PlannerTool
	// Instructions returns project-specific instructions appended to the
	// system prompt (the project's "planner" skill); optional.
	Instructions func(ctx context.Context, projectKey string) (string, error)
	Model        string
	MaxTokens    int
	MaxRounds    int // tool rounds per turn
	// CompactAt is the estimated context size (tokens) above which older
	// messages are summarized before calling the model; 0 disables.
	CompactAt int
	// OnCompact is called after each compaction (metrics); optional.
	OnCompact func()
	Now       func() time.Time
	NewID     func() string
	Logger    *slog.Logger
	// Context bounds every turn (Core's lifetime); turns outlive the
	// requests that start them.
	Context context.Context

	mu       sync.Mutex
	turns    map[string]context.CancelFunc // running turns by session
	watchers map[string]map[*PlannerWatch]struct{}
}

// SessionView is a session with its project's key.
type SessionView struct {
	planner.Session
	ProjectKey string
	Running    bool // a turn is in progress
}

// maxMessage bounds a human message.
const maxMessage = 100_000

// CreateSession starts a planner session in a project. Requires
// tracker.write: planning changes the project.
func (pl *Planner) CreateSession(ctx context.Context, projectKey, title string) (SessionView, error) {
	id, p, c, err := pl.authorizeProject(ctx, projectKey, ActTrackerWrite)
	if err != nil {
		return SessionView{}, err
	}
	if err := planner.ValidateTitle(title); err != nil {
		return SessionView{}, invalid(err)
	}
	now := pl.Now()
	s := planner.Session{ID: pl.NewID(), ProjectID: p.ID, Title: title, CreatedBy: id.Subject, CreatedAt: now, UpdatedAt: now}
	e := event.Event{Customer: c.ID, Project: p.ID, EntityType: "planner_session", EntityID: s.ID,
		Type: "planner.session_created", Actor: actorOf(id), OccurredAt: now, Payload: mustJSON(map[string]any{"title": title})}
	if err := pl.Store.CreatePlannerSession(ctx, s, e); err != nil {
		return SessionView{}, err
	}
	return SessionView{Session: s, ProjectKey: p.Key}, nil
}

// QuestionChat returns the sub-chat of a question in a project, starting
// it with context when there is none yet. Requires tracker.write.
func (pl *Planner) QuestionChat(ctx context.Context, projectKey, questionID, title, context string) (SessionView, error) {
	id, p, c, err := pl.authorizeProject(ctx, projectKey, ActTrackerWrite)
	if err != nil {
		return SessionView{}, err
	}
	if s, err := pl.Store.QuestionSession(ctx, questionID); err == nil {
		return SessionView{Session: s, ProjectKey: p.Key, Running: pl.running(s.ID)}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return SessionView{}, err
	}
	now := pl.Now()
	s := planner.Session{ID: pl.NewID(), ProjectID: p.ID, Title: title, CreatedBy: id.Subject, CreatedAt: now,
		UpdatedAt: now, QuestionID: questionID, Context: context}
	e := event.Event{Customer: c.ID, Project: p.ID, EntityType: "planner_session", EntityID: s.ID,
		Type: "planner.session_created", Actor: actorOf(id), OccurredAt: now,
		Payload: mustJSON(map[string]any{"title": title, "question": questionID})}
	if err := pl.Store.CreatePlannerSession(ctx, s, e); err != nil {
		return SessionView{}, err
	}
	return SessionView{Session: s, ProjectKey: p.Key}, nil
}

// ListSessions returns a project's sessions, most recently active first.
func (pl *Planner) ListSessions(ctx context.Context, projectKey string) ([]SessionView, error) {
	_, p, _, err := pl.authorizeProject(ctx, projectKey, ActTrackerRead)
	if err != nil {
		return nil, err
	}
	list, err := pl.Store.ListPlannerSessions(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionView, 0, len(list))
	for _, s := range list {
		out = append(out, SessionView{Session: s, ProjectKey: p.Key, Running: pl.running(s.ID)})
	}
	return out, nil
}

// Session returns a session and its transcript.
func (pl *Planner) Session(ctx context.Context, sessionID string) (SessionView, []planner.Message, error) {
	_, s, p, _, err := pl.authorizeSession(ctx, sessionID, ActTrackerRead)
	if err != nil {
		return SessionView{}, nil, err
	}
	msgs, err := pl.Store.PlannerMessages(ctx, s.ID)
	if err != nil {
		return SessionView{}, nil, err
	}
	return SessionView{Session: s, ProjectKey: p.Key, Running: pl.running(s.ID)}, msgs, nil
}

// Send stores a human message and starts the planner's turn in the
// background. Only humans talk to the planner; one turn runs per session
// at a time (ErrConflict otherwise).
func (pl *Planner) Send(ctx context.Context, sessionID, text string) (planner.Message, error) {
	id, s, p, c, err := pl.authorizeSession(ctx, sessionID, ActTrackerWrite)
	if err != nil {
		return planner.Message{}, err
	}
	if id.Kind != auth.KindHuman {
		return planner.Message{}, fmt.Errorf("%w: only humans talk to the planner", ErrForbidden)
	}
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > maxMessage {
		return planner.Message{}, fmt.Errorf("%w: a message must be 1-%d characters", ErrInvalid, maxMessage)
	}
	turn, cancel := context.WithCancel(ActingAsPlanner(auth.WithIdentity(pl.Context, id), s.ID))
	pl.mu.Lock()
	if pl.turns == nil {
		pl.turns = map[string]context.CancelFunc{}
	}
	if _, busy := pl.turns[s.ID]; busy {
		pl.mu.Unlock()
		cancel()
		return planner.Message{}, fmt.Errorf("%w: the planner is still answering", ErrConflict)
	}
	pl.turns[s.ID] = cancel
	pl.mu.Unlock()

	m, err := pl.appendMessage(ctx, s, c, planner.Message{
		SessionID: s.ID, Role: planner.RoleUser, Content: []planner.Block{planner.Text(text)}, Author: id.Subject,
	}, actorOf(id))
	if err != nil {
		pl.endTurn(s.ID)
		cancel()
		return planner.Message{}, err
	}
	go pl.turn(turn, s, p, c, id)
	return m, nil
}

// Cancel stops the session's running turn, if any.
func (pl *Planner) Cancel(ctx context.Context, sessionID string) error {
	if _, _, _, _, err := pl.authorizeSession(ctx, sessionID, ActTrackerWrite); err != nil {
		return err
	}
	pl.mu.Lock()
	cancel := pl.turns[sessionID]
	pl.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// PlannerWatch receives a session's live output until closed.
type PlannerWatch struct {
	pl      *Planner
	session string
	ch      chan PlannerOutput
	done    chan struct{}
	once    sync.Once
	lagging bool
}

// watchBuffer is how much output a slow watcher may fall behind before it
// is dropped (it then reloads the transcript).
const watchBuffer = 1024

// Watch delivers the session's live output to fn, in order, until the
// watch is closed. Returns whether a turn is running now. A watcher that
// falls too far behind is closed; Lagging then reports true.
func (pl *Planner) Watch(ctx context.Context, sessionID string, fn func(PlannerOutput)) (*PlannerWatch, bool, error) {
	if _, _, _, _, err := pl.authorizeSession(ctx, sessionID, ActTrackerRead); err != nil {
		return nil, false, err
	}
	w := &PlannerWatch{pl: pl, session: sessionID, ch: make(chan PlannerOutput, watchBuffer), done: make(chan struct{})}
	pl.mu.Lock()
	if pl.watchers == nil {
		pl.watchers = map[string]map[*PlannerWatch]struct{}{}
	}
	if pl.watchers[sessionID] == nil {
		pl.watchers[sessionID] = map[*PlannerWatch]struct{}{}
	}
	pl.watchers[sessionID][w] = struct{}{}
	_, running := pl.turns[sessionID]
	pl.mu.Unlock()
	go func() {
		for {
			select {
			case o := <-w.ch:
				fn(o)
			case <-w.done:
				return
			}
		}
	}()
	return w, running, nil
}

// Close stops the watch.
func (w *PlannerWatch) Close() {
	w.once.Do(func() {
		w.pl.mu.Lock()
		delete(w.pl.watchers[w.session], w)
		w.pl.mu.Unlock()
		close(w.done)
	})
}

// Done is closed when the watch ends.
func (w *PlannerWatch) Done() <-chan struct{} { return w.done }

// Lagging reports whether the watch was closed for falling behind.
func (w *PlannerWatch) Lagging() bool {
	w.pl.mu.Lock()
	defer w.pl.mu.Unlock()
	return w.lagging
}

func (pl *Planner) emit(o PlannerOutput) {
	pl.mu.Lock()
	var slow []*PlannerWatch
	for w := range pl.watchers[o.Session] {
		select {
		case w.ch <- o:
		default:
			w.lagging = true
			slow = append(slow, w)
		}
	}
	pl.mu.Unlock()
	for _, w := range slow {
		w.Close()
	}
}

func (pl *Planner) running(sessionID string) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	_, ok := pl.turns[sessionID]
	return ok
}

func (pl *Planner) endTurn(sessionID string) {
	pl.mu.Lock()
	delete(pl.turns, sessionID)
	pl.mu.Unlock()
}

// turn runs the conversation loop until the model stops calling tools,
// MaxRounds tool rounds are done, or the turn is cancelled.
func (pl *Planner) turn(ctx context.Context, s planner.Session, p tenancy.Project, c tenancy.Customer, human identity) {
	reason := "end_turn"
	defer func() {
		pl.endTurn(s.ID)
		pl.emit(PlannerOutput{Session: s.ID, Type: OutputDone, Text: reason})
	}()
	// Writes must not be lost to a cancellation of the turn.
	store := context.WithoutCancel(ctx)
	actor := event.Actor{Kind: event.ActorService, Subject: "planner:" + s.ID, ActingFor: human.Subject}
	fail := func(err error) {
		reason = "error"
		pl.logger().ErrorContext(store, "planner turn failed", "session", s.ID, "error", err)
		pl.emit(PlannerOutput{Session: s.ID, Type: OutputError, Text: err.Error()})
	}

	system := DefaultPlannerInstructions + fmt.Sprintf("\n\nProject: %s (%s), customer %s.", p.Name, p.Key, c.Key)
	if s.Context != "" {
		system += "\n\n" + s.Context
	}
	if pl.Instructions != nil {
		extra, err := pl.Instructions(ctx, p.Key)
		if err != nil {
			pl.logger().WarnContext(store, "planner instructions unavailable", "session", s.ID, "error", err)
		} else if extra != "" {
			system += "\n\n" + extra
		}
	}
	tools := map[string]PlannerTool{}
	specs := make([]ToolSpec, 0, len(pl.Tools))
	for _, t := range pl.Tools {
		tools[t.Spec().Name] = t
		specs = append(specs, t.Spec())
	}
	env := ToolEnv{CustomerKey: c.Key, ProjectKey: p.Key, SessionID: s.ID}

	for round := 0; ; round++ {
		history, err := pl.Store.PlannerMessages(store, s.ID)
		if err != nil {
			fail(err)
			return
		}
		caller := LLMCaller{CustomerKey: c.Key, ProjectKey: p.Key, SessionID: s.ID, ActingFor: human.Subject}
		if pl.CompactAt > 0 && estimateTokens(s, history) > pl.CompactAt {
			if err := pl.compact(ctx, &s, c, caller, history, actor); err != nil {
				if ctx.Err() != nil {
					reason = "cancelled"
					return
				}
				pl.logger().WarnContext(store, "planner compaction failed", "session", s.ID, "error", err)
			}
		}
		resp, err := pl.LLM.Stream(ctx, LLMRequest{
			Caller: caller, Model: pl.Model, System: system, Messages: modelInput(s, history), Tools: specs,
			MaxTokens: pl.MaxTokens,
		}, func(text string) {
			pl.emit(PlannerOutput{Session: s.ID, Type: OutputText, Text: text})
		})
		if ctx.Err() != nil {
			reason = "cancelled"
			return
		}
		if err != nil {
			fail(err)
			return
		}
		if _, err := pl.appendMessage(store, s, c, planner.Message{
			SessionID: s.ID, Role: planner.RoleAssistant, Content: resp.Content, StopReason: resp.StopReason, Usage: resp.Usage,
		}, actor); err != nil {
			fail(err)
			return
		}
		calls := planner.Message{Content: resp.Content}.ToolCalls()
		if resp.StopReason != "tool_use" || len(calls) == 0 {
			reason = resp.StopReason
			return
		}
		if round >= pl.maxRounds() {
			reason = "max_rounds"
			pl.emit(PlannerOutput{Session: s.ID, Type: OutputError,
				Text: fmt.Sprintf("stopped after %d tool rounds; send a message to continue", pl.maxRounds())})
			return
		}
		results := make([]planner.Block, 0, len(calls))
		for _, call := range calls {
			pl.emit(PlannerOutput{Session: s.ID, Type: OutputToolCall, Tool: call.Name, ToolUseID: call.ToolUseID, Input: call.Input})
			out, isErr := pl.callTool(ctx, tools, env, call)
			pl.emit(PlannerOutput{Session: s.ID, Type: OutputToolResult, Tool: call.Name, ToolUseID: call.ToolUseID, Text: out, IsError: isErr})
			results = append(results, planner.Block{Type: planner.BlockToolResult, ToolUseID: call.ToolUseID, Text: out, IsError: isErr})
		}
		if _, err := pl.appendMessage(store, s, c, planner.Message{
			SessionID: s.ID, Role: planner.RoleUser, Content: results,
		}, actor); err != nil {
			fail(err)
			return
		}
		if ctx.Err() != nil {
			reason = "cancelled"
			return
		}
	}
}

// summaryIntro introduces the compaction summary to the model.
const summaryIntro = "Summary of the earlier conversation (older messages are not shown):\n\n"

// compactInstructions is the system prompt of a compaction call.
const compactInstructions = `You maintain the memory of a planning conversation between a human and
the planner of a software project. Write a concise summary that lets the
planner continue without the earlier messages: goals and requirements,
decisions and their reasons, open questions, changesets proposed and their
outcome, knowledge entries written, item keys mentioned, and what was about
to happen next. Write plain Markdown; do not address the human.`

// after returns the messages after seq upTo.
func after(history []planner.Message, upTo int64) []planner.Message {
	for i, m := range history {
		if m.Seq > upTo {
			return history[i:]
		}
	}
	return nil
}

// modelInput is what the model sees: the summary (if compacted) and the
// messages after it.
func modelInput(s planner.Session, history []planner.Message) []planner.Message {
	msgs := after(history, s.SummaryUpTo)
	if s.Summary != "" {
		msgs = append([]planner.Message{{Role: planner.RoleUser, Content: []planner.Block{planner.Text(summaryIntro + s.Summary)}}}, msgs...)
	}
	return planner.RepairHistory(msgs)
}

// size is a rough token estimate of content (4 characters per token).
func size(msgs []planner.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			n += len(b.Text) + len(b.Input)
		}
	}
	return n / 4
}

// estimateTokens estimates the context the model would see: the usage the
// provider reported for the last answer plus the messages added since, or
// a character estimate when no usage is known.
func estimateTokens(s planner.Session, history []planner.Message) int {
	msgs := after(history, s.SummaryUpTo)
	for i := len(msgs) - 1; i >= 0; i-- {
		u := msgs[i].Usage
		if total := u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens + u.OutputTokens; msgs[i].Role == planner.RoleAssistant && total > 0 {
			return int(total) + size(msgs[i+1:])
		}
	}
	return size(msgs) + len(s.Summary)/4
}

// compact summarizes the messages before the latest human message into
// the session's summary. The current turn (from that message on) stays
// verbatim, so tool calls and their results are never split.
func (pl *Planner) compact(ctx context.Context, s *planner.Session, c tenancy.Customer, caller LLMCaller, history []planner.Message, actor event.Actor) error {
	msgs := after(history, s.SummaryUpTo)
	cut := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == planner.RoleUser && msgs[i].Author != "" {
			cut = i
			break
		}
	}
	if cut <= 0 {
		return nil // nothing before the current turn to summarize
	}
	old := msgs[:cut]
	input := append([]planner.Message(nil), old...)
	if s.Summary != "" {
		input = append([]planner.Message{{Role: planner.RoleUser, Content: []planner.Block{planner.Text(summaryIntro + s.Summary)}}}, input...)
	}
	input = append(input, planner.Message{Role: planner.RoleUser, Content: []planner.Block{planner.Text("Write the summary now.")}})
	resp, err := pl.LLM.Stream(ctx, LLMRequest{
		Caller: caller, Model: pl.Model, System: compactInstructions, Messages: planner.RepairHistory(input),
		MaxTokens: pl.MaxTokens,
	}, func(string) {})
	if err != nil {
		return err
	}
	var summary strings.Builder
	for _, b := range resp.Content {
		if b.Type == planner.BlockText {
			summary.WriteString(b.Text)
		}
	}
	if strings.TrimSpace(summary.String()) == "" {
		return errors.New("the model returned an empty summary")
	}
	upTo := old[len(old)-1].Seq
	e := event.Event{Customer: c.ID, Project: s.ProjectID, EntityType: "planner_session", EntityID: s.ID,
		Type: "planner.compacted", Actor: actor, OccurredAt: pl.Now(),
		Payload: mustJSON(map[string]any{"up_to": upTo, "estimated_tokens": estimateTokens(*s, history)})}
	if err := pl.Store.SetPlannerSummary(context.WithoutCancel(ctx), s.ID, summary.String(), upTo, e); err != nil {
		return err
	}
	s.Summary, s.SummaryUpTo = summary.String(), upTo
	pl.emit(PlannerOutput{Session: s.ID, Type: OutputCompacted, Seq: upTo})
	if pl.OnCompact != nil {
		pl.OnCompact()
	}
	return nil
}

// callTool runs one tool call; failures become error results the model
// can react to.
func (pl *Planner) callTool(ctx context.Context, tools map[string]PlannerTool, env ToolEnv, call planner.Block) (string, bool) {
	t, ok := tools[call.Name]
	if !ok {
		return fmt.Sprintf("unknown tool %q", call.Name), true
	}
	input := call.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	out, err := t.Call(ctx, env, input)
	if err != nil {
		return toolError(err), true
	}
	return out, false
}

// toolError describes a failed tool call to the model without leaking
// internals.
func toolError(err error) string {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden: the human you act for may not do this"
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalid), errors.Is(err, ErrConflict), errors.Is(err, ErrAlreadyExists):
		return err.Error()
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return "internal error"
}

func (pl *Planner) appendMessage(ctx context.Context, s planner.Session, c tenancy.Customer, m planner.Message, actor event.Actor) (planner.Message, error) {
	m.CreatedAt = pl.Now()
	e := event.Event{Customer: c.ID, Project: s.ProjectID, EntityType: "planner_session", EntityID: s.ID,
		Type: "planner.message", Actor: actor, OccurredAt: m.CreatedAt, Payload: mustJSON(map[string]any{"role": m.Role})}
	stored, err := pl.Store.AppendPlannerMessage(ctx, m, e)
	if err != nil {
		return planner.Message{}, err
	}
	pl.emit(PlannerOutput{Session: s.ID, Type: OutputMessage, Seq: stored.Seq})
	return stored, nil
}

func (pl *Planner) authorizeProject(ctx context.Context, projectKey string, a Action) (identity, tenancy.Project, tenancy.Customer, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	p, err := pl.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	c, err := pl.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return identity{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	if err := pl.Authz.Authorize(ctx, id, a, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return identity{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	return id, p, c, nil
}

func (pl *Planner) authorizeSession(ctx context.Context, sessionID string, a Action) (identity, planner.Session, tenancy.Project, tenancy.Customer, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, planner.Session{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	s, err := pl.Store.PlannerSession(ctx, sessionID)
	if err != nil {
		return identity{}, planner.Session{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	p, err := pl.Tenancy.ProjectByID(ctx, s.ProjectID)
	if err != nil {
		return identity{}, planner.Session{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	c, err := pl.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return identity{}, planner.Session{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	if err := pl.Authz.Authorize(ctx, id, a, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return identity{}, planner.Session{}, tenancy.Project{}, tenancy.Customer{}, err
	}
	return id, s, p, c, nil
}

// DefaultMaxRounds bounds tool rounds per turn when MaxRounds is unset.
const DefaultMaxRounds = 20

func (pl *Planner) maxRounds() int {
	if pl.MaxRounds > 0 {
		return pl.MaxRounds
	}
	return DefaultMaxRounds
}

func (pl *Planner) logger() *slog.Logger {
	if pl.Logger != nil {
		return pl.Logger
	}
	return slog.Default()
}
