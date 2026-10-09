package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// QuestionStore persists questions.
type QuestionStore interface {
	Question(ctx context.Context, id string) (report.Question, error)
	Questions(ctx context.Context, ticketID string) ([]report.Question, error)
	// RouteQuestion sets an open question's route (ErrConflict when it is
	// answered).
	RouteQuestion(ctx context.Context, id, route string, e event.Event) error
	// AnswerQuestion answers an open question (ErrConflict when it is
	// answered already) and enqueues jobs, atomically.
	AnswerQuestion(ctx context.Context, q report.Question, jobs []Job, e event.Event) error
}

// QuestionKnowledge is the knowledge base as Core itself uses it.
type QuestionKnowledge interface {
	// Search finds entries of an organization's knowledge about project.
	Search(ctx context.Context, organization, project, query string, limit int) ([]onboarding.Knowledge, error)
	// RecordAnswer writes an answered question to the knowledge base,
	// linked to the ticket, as author.
	RecordAnswer(ctx context.Context, organization, project, ticketKey, author string, q report.Question) error
}

// InboxStore finds what the humans' inbox shows.
type InboxStore interface {
	OpenQuestions(ctx context.Context) ([]report.Question, error)
	// BlockedBehind counts the unresolved items an item blocks, directly
	// or transitively.
	BlockedBehind(ctx context.Context, itemID string) (int, error)
}

// JobQuestionRoute routes a raised question: the planner tries to answer
// it, else it goes to the humans.
const JobQuestionRoute = "question.route"

// Questions answers the questions runs raise (ADR-0015): the planner first,
// from cited sources only, then the humans. An answer is written to the
// knowledge base and resumes the ticket's flow.
type Questions struct {
	Store        QuestionStore
	Items        ItemStore
	Tenancy      TenancyStore
	Authz        Authorizer
	Orchestrator *Orchestrator
	Knowledge    QuestionKnowledge // nil: no knowledge search, answers not recorded
	LLM          LLM               // nil: every question goes to the humans
	Inbox        InboxStore        // nil: no inbox
	Reports      ReportStore       // stage reports for sub-chats; optional
	Planner      *Planner          // sub-chats; nil: none
	// OnAnswered is called after a question was answered: the answer goes
	// to the session that asked it if it still waits (Runs.DeliverAnswer);
	// optional.
	OnAnswered func(ctx context.Context, q report.Question)
	Model      string
	MaxTokens  int
	MaxRounds  int // tool rounds of an answer attempt; default 8
	Now        func() time.Time
	NewID      func() string
	Logger     *slog.Logger
}

// QuestionView is a question with its ticket's key.
type QuestionView struct {
	report.Question
	TicketKey string
}

type questionJob struct {
	QuestionID string `json:"question_id"`
}

// Register installs the routing job handler.
func (qs *Questions) Register(o *Orchestrator) {
	o.Handle(JobQuestionRoute, qs.handleRoute)
}

// Route queues the routing of a question a run raised (AgentTracker.OnQuestion).
func (qs *Questions) Route(ctx context.Context, q report.Question) {
	j := NewJob(qs.NewID(), JobQuestionRoute, "question.route:"+q.ID, questionJob{QuestionID: q.ID}, qs.Now(), 3)
	if err := qs.Orchestrator.Enqueue(ctx, j); err != nil {
		qs.logger().ErrorContext(ctx, "enqueue question routing failed", "question", q.ID, "error", err)
	}
}

// Answer answers an open question as the human caller (tracker.write).
func (qs *Questions) Answer(ctx context.Context, questionID, text string) (QuestionView, error) {
	id, err := caller(ctx)
	if err != nil {
		return QuestionView{}, err
	}
	q, err := qs.Store.Question(ctx, questionID)
	if err != nil {
		return QuestionView{}, err
	}
	it, p, c, err := qs.scope(ctx, q)
	if err != nil {
		return QuestionView{}, err
	}
	if err := qs.Authz.Authorize(ctx, id, ActTrackerWrite, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return QuestionView{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > report.MaxText {
		return QuestionView{}, fmt.Errorf("%w: answer must be 1-%d characters", ErrInvalid, report.MaxText)
	}
	if q.Status != report.QuestionOpen {
		return QuestionView{}, fmt.Errorf("%w: the question is answered already", ErrConflict)
	}
	return qs.answer(ctx, q, it, p, c, text, id.Subject, actorIn(ctx, id))
}

func (qs *Questions) answer(ctx context.Context, q report.Question, it tracker.Item, p tenancy.Project, c tenancy.Organization,
	text, by string, actor event.Actor) (QuestionView, error) {
	q.Status, q.Answer, q.AnsweredBy, q.AnsweredAt = report.QuestionAnswered, text, by, qs.Now()
	var jobs []Job
	if q.Blocking {
		// No dedupe key: a resume job running right now may have read the
		// question still open. Resuming is idempotent.
		jobs = append(jobs, NewJob(qs.NewID(), JobFlowResume, "", flowJob{TicketID: it.ID}, qs.Now(), 10))
	}
	e := event.Event{Organization: c.ID, Project: p.ID, EntityType: "item", EntityID: it.ID, Type: "item.question_answered",
		Actor: actor, OccurredAt: qs.Now(), Payload: mustJSON(map[string]any{"question": q.ID, "by": by})}
	if err := qs.Store.AnswerQuestion(ctx, q, jobs, e); err != nil {
		return QuestionView{}, err
	}
	if len(jobs) > 0 {
		qs.Orchestrator.Kick()
	}
	if qs.OnAnswered != nil {
		qs.OnAnswered(ctx, q)
	}
	if qs.Knowledge != nil {
		if err := qs.Knowledge.RecordAnswer(ctx, c.Key, p.Key, it.Key, by, q); err != nil {
			qs.logger().WarnContext(ctx, "recording the answer in the knowledge base failed", "question", q.ID, "error", err)
		}
	}
	return QuestionView{Question: q, TicketKey: it.Key}, nil
}

func (qs *Questions) scope(ctx context.Context, q report.Question) (tracker.Item, tenancy.Project, tenancy.Organization, error) {
	it, err := qs.Items.ItemByID(ctx, q.TicketID)
	if err != nil {
		return tracker.Item{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	p, err := qs.Tenancy.ProjectByID(ctx, q.ProjectID)
	if err != nil {
		return tracker.Item{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	c, err := qs.Tenancy.OrganizationByID(ctx, p.OrganizationID)
	return it, p, c, err
}

func (qs *Questions) handleRoute(ctx context.Context, j Job) error {
	var p questionJob
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	q, err := qs.Store.Question(ctx, p.QuestionID)
	if err != nil {
		return err
	}
	if q.Status != report.QuestionOpen || q.Route == report.RouteHuman {
		return nil
	}
	it, pr, c, err := qs.scope(ctx, q)
	if err != nil {
		return err
	}
	if qs.LLM == nil {
		return qs.escalate(ctx, q, it, c, "No planner is configured.")
	}
	if err := qs.Store.RouteQuestion(ctx, q.ID, report.RoutePlanner, qs.event(it, c, "item.question_routed",
		map[string]any{"question": q.ID, "to": report.RoutePlanner})); err != nil {
		return ignoreConflict(err)
	}
	text, reason, err := qs.attempt(ctx, q, it, pr, c)
	switch {
	case err != nil:
		return qs.escalate(ctx, q, it, c, "The planner failed: "+err.Error())
	case text == "":
		return qs.escalate(ctx, q, it, c, reason)
	}
	_, err = qs.answer(ctx, q, it, pr, c, text, "planner", event.Actor{Kind: event.ActorService, Subject: "planner:question-" + q.ID})
	return ignoreConflict(err) // a human answered meanwhile
}

func (qs *Questions) escalate(ctx context.Context, q report.Question, it tracker.Item, c tenancy.Organization, reason string) error {
	e := qs.event(it, c, "item.question_escalated", map[string]any{"question": q.ID, "reason": reason})
	return ignoreConflict(qs.Store.RouteQuestion(ctx, q.ID, report.RouteHuman, e))
}

func (qs *Questions) event(it tracker.Item, c tenancy.Organization, typ string, payload map[string]any) event.Event {
	return event.Event{Organization: c.ID, Project: it.ProjectID, EntityType: "item", EntityID: it.ID, Type: typ,
		Actor: event.System, OccurredAt: qs.Now(), Payload: mustJSON(payload)}
}

// QuestionInstructions is the planner's system prompt for answering a
// question.
const QuestionInstructions = `You are the planner of a software project in Ballet. An AI agent working on
a ticket asked the question below. Answer it only from sources you can
cite: knowledge entries (search_knowledge) and tracker items (get_item).
Never guess. Escalate to the humans what the sources do not settle, and
always what concerns product intent, scope, priorities, money, credentials
or anything hard to undo.

Finish by calling exactly one tool: answer (the answer, and the IDs or keys
of the sources it rests on) or escalate (why the sources do not answer it).`

// attempt lets the planner try to answer q. It returns the answer with its
// sources, or the reason to escalate.
func (qs *Questions) attempt(ctx context.Context, q report.Question, it tracker.Item, p tenancy.Project,
	c tenancy.Organization) (answer, reason string, err error) {
	rounds := qs.MaxRounds
	if rounds <= 0 {
		rounds = 8
	}
	maxTokens := qs.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "# Ticket %s: %s\n\n%s\n", it.Key, it.Title, strings.TrimSpace(it.Description))
	if len(it.AcceptanceCriteria) > 0 {
		prompt.WriteString("\nAcceptance criteria:\n")
		for _, ac := range it.AcceptanceCriteria {
			prompt.WriteString("- " + ac + "\n")
		}
	}
	fmt.Fprintf(&prompt, "\n# Question\n\n%s\n", q.Text)
	if strings.TrimSpace(q.Context) != "" {
		fmt.Fprintf(&prompt, "\n# What the agent knows\n\n%s\n", q.Context)
	}
	msgs := []planner.Message{{Role: planner.RoleUser, Content: []planner.Block{planner.Text(prompt.String())}}}
	req := LLMRequest{Caller: LLMCaller{OrganizationKey: c.Key, ProjectKey: p.Key, SessionID: "question-" + q.ID, TicketKey: it.Key},
		Model: qs.Model, System: QuestionInstructions, Tools: questionTools(qs.Knowledge != nil), MaxTokens: maxTokens}
	for range rounds {
		req.Messages = msgs
		resp, err := qs.LLM.Stream(ctx, req, func(string) {})
		if err != nil {
			return "", "", err
		}
		msgs = append(msgs, planner.Message{Role: planner.RoleAssistant, Content: resp.Content})
		var results []planner.Block
		for _, b := range resp.Content {
			if b.Type != planner.BlockToolUse {
				continue
			}
			switch b.Name {
			case "answer":
				var in struct {
					Answer  string   `json:"answer"`
					Sources []string `json:"sources"`
				}
				if json.Unmarshal(b.Input, &in) == nil && strings.TrimSpace(in.Answer) != "" && len(in.Sources) > 0 {
					return fmt.Sprintf("%s\n\nSources: %s", strings.TrimSpace(in.Answer), strings.Join(in.Sources, ", ")), "", nil
				}
				results = append(results, planner.Block{Type: planner.BlockToolResult, ToolUseID: b.ToolUseID, IsError: true,
					Text: "An answer needs text and at least one source; otherwise escalate."})
			case "escalate":
				var in struct {
					Reason string `json:"reason"`
				}
				_ = json.Unmarshal(b.Input, &in)
				return "", "The planner escalated: " + strings.TrimSpace(in.Reason), nil
			default:
				out, err := qs.tool(ctx, b, it, p, c)
				res := planner.Block{Type: planner.BlockToolResult, ToolUseID: b.ToolUseID, Text: out}
				if err != nil {
					res.Text, res.IsError = err.Error(), true
				}
				results = append(results, res)
			}
		}
		if len(results) == 0 {
			return "", "The planner did not decide.", nil
		}
		msgs = append(msgs, planner.Message{Role: planner.RoleUser, Content: results})
	}
	return "", "The planner did not decide within its rounds.", nil
}

func (qs *Questions) tool(ctx context.Context, b planner.Block, it tracker.Item, p tenancy.Project, c tenancy.Organization) (string, error) {
	switch b.Name {
	case "search_knowledge":
		var in struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(b.Input, &in); err != nil || qs.Knowledge == nil {
			return "", errors.New("bad input")
		}
		found, err := qs.Knowledge.Search(ctx, c.Key, p.Key, in.Query, 5)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(found)
		return string(data), err
	case "get_item":
		var in struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(b.Input, &in); err != nil {
			return "", errors.New("bad input")
		}
		x, err := qs.Items.ItemByKey(ctx, in.Key)
		if err != nil || x.ProjectID != it.ProjectID {
			return "", fmt.Errorf("no item %s in project %s", in.Key, p.Key)
		}
		data, err := json.Marshal(map[string]any{"key": x.Key, "kind": x.Kind, "title": x.Title, "state": x.State,
			"description": x.Description, "acceptance_criteria": x.AcceptanceCriteria})
		return string(data), err
	}
	return "", fmt.Errorf("unknown tool %s", b.Name)
}

func questionTools(knowledge bool) []ToolSpec {
	tools := []ToolSpec{
		{Name: "get_item", Description: "Read a milestone, epic or ticket of the project by key, e.g. WEB-12.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`)},
		{Name: "answer", Description: "Answer the question, citing the knowledge entry IDs or item keys it rests on.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"},` +
				`"sources":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["answer","sources"]}`)},
		{Name: "escalate", Description: "Hand the question to the humans, saying why the sources do not answer it.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"}},"required":["reason"]}`)},
	}
	if knowledge {
		tools = append(tools, ToolSpec{Name: "search_knowledge",
			Description: "Search the knowledge base (documents, decisions, notes) of the project; returns whole entries.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)})
	}
	return tools
}

// InboxEntry is an open question as the inbox shows it.
type InboxEntry struct {
	QuestionView
	OrganizationKey string
	ProjectKey      string
	TicketTitle     string
	TicketState     tracker.State
	BlockedBehind   int    // unresolved items waiting behind the ticket
	Chat            string // the question's sub-chat session, if any
}

// ListInbox returns the open questions of every project the caller can
// read, most impactful first: those waiting for a human before those the
// planner works on, blocking ones first, then by how much work waits
// behind the ticket, then the oldest.
func (qs *Questions) ListInbox(ctx context.Context) ([]InboxEntry, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	open, err := qs.Inbox.OpenQuestions(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	out := []InboxEntry{}
	for _, q := range open {
		ok, seen := allowed[q.ProjectID]
		it, p, c, err := qs.scope(ctx, q)
		if err != nil {
			return nil, err
		}
		if !seen {
			ok = qs.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Organization: c.Key, Project: p.Key}) == nil
			allowed[q.ProjectID] = ok
		}
		if !ok {
			continue
		}
		n, err := qs.Inbox.BlockedBehind(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		e := InboxEntry{QuestionView: QuestionView{Question: q, TicketKey: it.Key}, OrganizationKey: c.Key, ProjectKey: p.Key,
			TicketTitle: it.Title, TicketState: it.State, BlockedBehind: n}
		if qs.Planner != nil {
			if s, err := qs.Planner.Store.QuestionSession(ctx, q.ID); err == nil {
				e.Chat = s.ID
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Route == report.RouteHuman) != (b.Route == report.RouteHuman) {
			return a.Route == report.RouteHuman
		}
		if a.Blocking != b.Blocking {
			return a.Blocking
		}
		if a.BlockedBehind != b.BlockedBehind {
			return a.BlockedBehind > b.BlockedBehind
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return out, nil
}

// QuestionChatInstructions start a question's sub-chat.
const QuestionChatInstructions = `This conversation is the sub-chat of a question an AI agent (or Ballet)
asked while working on a ticket. Help the human decide the answer: explain
the question, research the knowledge base and the plan, lay out the
options and their consequences, and suggest an answer. You cannot answer
for the human: they record the answer in the inbox, which resumes the
ticket. Propose changesets only when the human asks for plan changes.`

// Chat returns the question's sub-chat with the planner, starting it with
// the question's context (tracker.write).
func (qs *Questions) Chat(ctx context.Context, questionID string) (SessionView, error) {
	if qs.Planner == nil {
		return SessionView{}, fmt.Errorf("%w: no planner", ErrUnavailable)
	}
	if _, err := caller(ctx); err != nil {
		return SessionView{}, err
	}
	q, err := qs.Store.Question(ctx, questionID)
	if err != nil {
		return SessionView{}, err
	}
	it, p, _, err := qs.scope(ctx, q)
	if err != nil {
		return SessionView{}, err
	}
	var b strings.Builder
	b.WriteString(QuestionChatInstructions)
	fmt.Fprintf(&b, "\n\n# Ticket %s: %s (%s)\n\n%s\n", it.Key, it.Title, it.State, strings.TrimSpace(it.Description))
	for _, ac := range it.AcceptanceCriteria {
		b.WriteString("- [ ] " + ac + "\n")
	}
	fmt.Fprintf(&b, "\n# Question (%s)\n\n%s\n", map[bool]string{true: "blocking", false: "not blocking"}[q.Blocking], q.Text)
	if strings.TrimSpace(q.Context) != "" {
		fmt.Fprintf(&b, "\n# Context given with it\n\n%s\n", q.Context)
	}
	if qs.Reports != nil {
		reports, err := qs.Reports.Reports(ctx, it.ID, "")
		if err != nil {
			return SessionView{}, err
		}
		var stage []report.Report
		for _, r := range reports {
			if r.Kind == report.KindStageReport {
				stage = append(stage, r)
			}
		}
		if len(stage) > 3 {
			stage = stage[len(stage)-3:]
		}
		if len(stage) > 0 {
			b.WriteString("\n# Latest stage reports\n")
			for _, r := range stage {
				fmt.Fprintf(&b, "\n- (%s) %s\n", r.Outcome, oneLine(r.Text, 600))
			}
		}
	}
	title := fmt.Sprintf("Question on %s: %s", it.Key, oneLine(q.Text, 120))
	return qs.Planner.QuestionChat(ctx, p.Key, q.ID, title, b.String())
}

func (qs *Questions) logger() *slog.Logger {
	if qs.Logger != nil {
		return qs.Logger
	}
	return slog.Default()
}
