// Package trackermcp serves the tracker to agent runs over MCP (streamable
// HTTP at /mcp/tracker): the run's ticket context and reporting on it.
// Runs authenticate with their run token (audience "core"); every tool
// works on the token's ticket only.
package trackermcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Path of the MCP endpoint.
const Path = "/mcp/tracker"

// Register mounts the tracker MCP endpoint on mux.
func Register(mux *http.ServeMux, v *runtoken.Verifier, at *app.AgentTracker, version string) {
	server := NewServer(at, version)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true})
	verify := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		c, err := v.Verify(ctx, token, "core")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
		}
		if c.Kind != runtoken.KindRun {
			return nil, fmt.Errorf("%w: a run token is required", auth.ErrInvalidToken)
		}
		return &auth.TokenInfo{Scopes: c.Capabilities, Expiration: c.Expiry, UserID: c.Subject,
			Extra: map[string]any{"claims": c}}, nil
	}
	mux.Handle(Path, auth.RequireBearerToken(verify, nil)(handler))
}

// NewServer builds the MCP server with the tracker tools.
func NewServer(at *app.AgentTracker, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ballet-tracker", Version: version}, &mcp.ServerOptions{
		Instructions: "Ballet tracker for your ticket. Read ticket_context first. Report progress as you go, " +
			"record assumptions you make, raise a question when you cannot decide, propose work you discover " +
			"instead of doing it, and end your stage with submit_stage_report.",
	})
	t := tools{at: at}
	mcp.AddTool(server, &mcp.Tool{Name: "ticket_context",
		Description: "Your ticket with its plan context, dependencies, knowledge, and the reports and questions so far."}, t.context)
	mcp.AddTool(server, &mcp.Tool{Name: "report_progress",
		Description: "Tell the humans what you are doing or have done, in one or two sentences."}, t.progress)
	mcp.AddTool(server, &mcp.Tool{Name: "submit_stage_report",
		Description: "End your stage: outcome done (the stage's work is complete), blocked (you need help) or failed; " +
			"a summary and details. Submit exactly once, at the end."}, t.stageReport)
	mcp.AddTool(server, &mcp.Tool{Name: "record_assumption",
		Description: "Record a reversible, low-impact decision you made without asking, with its rationale."}, t.assumption)
	mcp.AddTool(server, &mcp.Tool{Name: "raise_question",
		Description: "Ask the planner and the humans something you cannot decide. Set blocking when you cannot " +
			"continue without the answer: then push your work in progress, submit your stage report and end the " +
			"session; a new session of this stage continues with the answer."}, t.question)
	mcp.AddTool(server, &mcp.Tool{Name: "propose_work",
		Description: "Propose a new ticket for work you discovered but should not do in this ticket; the planner " +
			"and the humans decide. You never create tickets."}, t.propose)
	return server
}

type tools struct{ at *app.AgentTracker }

// caller returns the run calling, checking it holds capability.
func caller(req *mcp.CallToolRequest, capability string) (app.RunCaller, error) {
	var c runtoken.Claims
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		c, _ = req.Extra.TokenInfo.Extra["claims"].(runtoken.Claims)
	}
	runID, ok := strings.CutPrefix(c.Subject, "run:")
	if !ok || c.Ticket == "" {
		return app.RunCaller{}, errors.New("a run token is required")
	}
	if !c.Can(capability) {
		return app.RunCaller{}, fmt.Errorf("the run token lacks %s", capability)
	}
	return app.RunCaller{RunID: runID, Customer: c.Customer, Project: c.Project, Ticket: c.Ticket}, nil
}

// toolErr turns errors into tool errors the agent can read.
func toolErr(err error) error {
	switch {
	case errors.Is(err, app.ErrForbidden), errors.Is(err, app.ErrInvalid), errors.Is(err, app.ErrNotFound):
		return err
	}
	return errors.New("internal error")
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

type empty struct{}

func (t tools) context(ctx context.Context, req *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	c, err := caller(req, runtoken.CapTrackerRead)
	if err != nil {
		return nil, nil, err
	}
	s, err := t.at.Context(ctx, c)
	if err != nil {
		return nil, nil, toolErr(err)
	}
	return text(s), nil, nil
}

// ProgressInput is the input of report_progress.
type ProgressInput struct {
	Message string `json:"message" jsonschema:"what you are doing or have done"`
}

// Recorded confirms a report.
type Recorded struct {
	ID string `json:"id"`
}

func (t tools) progress(ctx context.Context, req *mcp.CallToolRequest, in ProgressInput) (*mcp.CallToolResult, Recorded, error) {
	c, err := caller(req, runtoken.CapTrackerReport)
	if err != nil {
		return nil, Recorded{}, err
	}
	r, err := t.at.Report(ctx, c, report.KindProgress, "", in.Message, "")
	if err != nil {
		return nil, Recorded{}, toolErr(err)
	}
	return nil, Recorded{ID: r.ID}, nil
}

// StageReportInput is the input of submit_stage_report.
type StageReportInput struct {
	Outcome string `json:"outcome" jsonschema:"done, blocked or failed"`
	Summary string `json:"summary" jsonschema:"what you did and what is left, in a few sentences"`
	Details string `json:"details,omitempty" jsonschema:"Markdown: changes, test results, follow-ups"`
}

func (t tools) stageReport(ctx context.Context, req *mcp.CallToolRequest, in StageReportInput) (*mcp.CallToolResult, Recorded, error) {
	c, err := caller(req, runtoken.CapTrackerReport)
	if err != nil {
		return nil, Recorded{}, err
	}
	r, err := t.at.Report(ctx, c, report.KindStageReport, report.Outcome(in.Outcome), in.Summary, in.Details)
	if err != nil {
		return nil, Recorded{}, toolErr(err)
	}
	return nil, Recorded{ID: r.ID}, nil
}

// AssumptionInput is the input of record_assumption.
type AssumptionInput struct {
	Assumption string `json:"assumption"`
	Rationale  string `json:"rationale,omitempty"`
}

func (t tools) assumption(ctx context.Context, req *mcp.CallToolRequest, in AssumptionInput) (*mcp.CallToolResult, Recorded, error) {
	c, err := caller(req, runtoken.CapTrackerReport)
	if err != nil {
		return nil, Recorded{}, err
	}
	r, err := t.at.Report(ctx, c, report.KindAssumption, "", in.Assumption, in.Rationale)
	if err != nil {
		return nil, Recorded{}, toolErr(err)
	}
	return nil, Recorded{ID: r.ID}, nil
}

// QuestionInput is the input of raise_question.
type QuestionInput struct {
	Question string `json:"question"`
	Context  string `json:"context,omitempty" jsonschema:"what you know and the options you see"`
	Blocking bool   `json:"blocking,omitempty" jsonschema:"true when you cannot continue without the answer"`
}

// QuestionRecorded is the result of raise_question.
type QuestionRecorded struct {
	ID   string `json:"id"`
	Next string `json:"next"` // what the agent does now
}

func (t tools) question(ctx context.Context, req *mcp.CallToolRequest, in QuestionInput) (*mcp.CallToolResult, QuestionRecorded, error) {
	c, err := caller(req, runtoken.CapTrackerReport)
	if err != nil {
		return nil, QuestionRecorded{}, err
	}
	q, err := t.at.RaiseQuestion(ctx, c, in.Question, in.Context, in.Blocking)
	if err != nil {
		return nil, QuestionRecorded{}, toolErr(err)
	}
	next := "Continue; the answer will appear in ticket_context."
	if in.Blocking {
		next = "Push your work in progress, submit your stage report (outcome blocked) and end the session now. " +
			"A new session of this stage continues once the question is answered."
	}
	return nil, QuestionRecorded{ID: q.ID, Next: next}, nil
}

// ProposeInput is the input of propose_work.
type ProposeInput struct {
	Title       string `json:"title"`
	Description string `json:"description" jsonschema:"what the work is, in Markdown"`
	Type        string `json:"type,omitempty" jsonschema:"feature, bug, tech_debt, docs or spike"`
	Reason      string `json:"reason" jsonschema:"why it is needed and how you found it"`
}

// Proposed confirms a proposal.
type Proposed struct {
	Changeset string `json:"changeset"`
	Note      string `json:"note"`
}

func (t tools) propose(ctx context.Context, req *mcp.CallToolRequest, in ProposeInput) (*mcp.CallToolResult, Proposed, error) {
	c, err := caller(req, runtoken.CapTrackerReport)
	if err != nil {
		return nil, Proposed{}, err
	}
	v, err := t.at.ProposeWork(ctx, c, in.Title, in.Description, tracker.TicketType(in.Type), in.Reason)
	if err != nil {
		return nil, Proposed{}, toolErr(err)
	}
	return nil, Proposed{Changeset: v.ID, Note: "Proposed; the planner and the humans decide. Do not do this work now."}, nil
}
