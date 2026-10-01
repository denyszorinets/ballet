// Package mcpapi exposes the knowledge space to agents over MCP
// (streamable HTTP at /mcp, ADR-0005). Agents authenticate with their run
// token (audience "knowledge"); every tool works in the token's customer
// space with the token's capabilities.
package mcpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/domain"
)

// Path of the MCP endpoint.
const Path = "/mcp"

// Register mounts the MCP endpoint on mux.
func Register(mux *http.ServeMux, v *runtoken.Verifier, s *app.Service, version string) {
	server := NewServer(s, version)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true})
	verify := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		c, err := v.Verify(ctx, token, "knowledge")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
		}
		return &auth.TokenInfo{Scopes: c.Capabilities, Expiration: c.Expiry, UserID: c.Subject,
			Extra: map[string]any{"claims": c}}, nil
	}
	mux.Handle(Path, auth.RequireBearerToken(verify, nil)(handler))
}

// NewServer builds the MCP server with Ballet's knowledge tools.
func NewServer(s *app.Service, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ballet-knowledge", Version: version}, &mcp.ServerOptions{
		Instructions: "Ballet knowledge base of this customer: documentation, decisions, notes and technical debt. " +
			"Search it before starting work and record what you learn, decide or leave unfinished.",
	})
	t := tools{s: s}
	mcp.AddTool(server, &mcp.Tool{Name: "knowledge_search",
		Description: "Hybrid full-text and semantic search in the customer's knowledge base."}, t.search)
	mcp.AddTool(server, &mcp.Tool{Name: "knowledge_list",
		Description: "List entries, optionally filtered by kind, project or linked tracker item (e.g. WEB-42)."}, t.list)
	mcp.AddTool(server, &mcp.Tool{Name: "knowledge_get",
		Description: "Read one entry in full."}, t.get)
	mcp.AddTool(server, &mcp.Tool{Name: "knowledge_create",
		Description: "Create a document, decision, note or debt record. Defaults the project to the run's project."}, t.create)
	mcp.AddTool(server, &mcp.Tool{Name: "knowledge_update",
		Description: "Update an entry; pass the version you read. Every update keeps the previous version."}, t.update)
	return server
}

type tools struct{ s *app.Service }

// scope returns a context carrying the caller's claims and its customer.
func scope(ctx context.Context, req *mcp.CallToolRequest) (context.Context, runtoken.Claims, error) {
	var c runtoken.Claims
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		c, _ = req.Extra.TokenInfo.Extra["claims"].(runtoken.Claims)
	}
	if c.Customer == "" {
		return nil, c, errors.New("token is not scoped to a customer")
	}
	return runtoken.ContextWithClaims(ctx, c), c, nil
}

// EntrySummary is an entry without its full body.
type EntrySummary struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Snippet  string   `json:"snippet" jsonschema:"start of the body"`
	Projects []string `json:"projects"`
	Items    []string `json:"items"`
	Version  int64    `json:"version"`
	Score    float64  `json:"score,omitempty" jsonschema:"search relevance; higher is better"`
}

// Entry is a full entry.
type Entry struct {
	EntrySummary
	Body      string `json:"body" jsonschema:"Markdown"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

func summary(e domain.Entry, score float64) EntrySummary {
	snippet := e.Body
	if utf8.RuneCountInString(snippet) > 300 {
		snippet = string([]rune(snippet)[:300]) + "…"
	}
	nz := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	return EntrySummary{ID: e.ID, Kind: string(e.Kind), Title: e.Title, Snippet: snippet,
		Projects: nz(e.Projects), Items: nz(e.Items), Version: e.Version, Score: score}
}

func full(e domain.Entry) Entry {
	s := summary(e, 0)
	return Entry{EntrySummary: s, Body: e.Body, UpdatedBy: e.UpdatedBy, UpdatedAt: e.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")}
}

// SearchInput is the input of knowledge_search.
type SearchInput struct {
	Query   string `json:"query" jsonschema:"what to look for, in plain words"`
	Kind    string `json:"kind,omitempty" jsonschema:"document, decision, note or debt"`
	Project string `json:"project,omitempty" jsonschema:"project key"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum results (default 10)"`
}

// Entries is a list result.
type Entries struct {
	Entries []EntrySummary `json:"entries"`
}

func (t tools) search(ctx context.Context, req *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, Entries, error) {
	ctx, c, err := scope(ctx, req)
	if err != nil {
		return nil, Entries{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	hits, err := t.s.Search(ctx, c.Customer, app.SearchQuery{Text: in.Query, Kind: domain.Kind(in.Kind), Project: in.Project, Limit: limit})
	if err != nil {
		return nil, Entries{}, err
	}
	out := Entries{Entries: []EntrySummary{}}
	for _, h := range hits {
		out.Entries = append(out.Entries, summary(h.Entry, h.Score))
	}
	return nil, out, nil
}

// ListInput is the input of knowledge_list.
type ListInput struct {
	Kind    string `json:"kind,omitempty" jsonschema:"document, decision, note or debt"`
	Project string `json:"project,omitempty" jsonschema:"project key"`
	Item    string `json:"item,omitempty" jsonschema:"linked tracker item key, e.g. WEB-42"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum results (default 50)"`
}

func (t tools) list(ctx context.Context, req *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, Entries, error) {
	ctx, c, err := scope(ctx, req)
	if err != nil {
		return nil, Entries{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	list, err := t.s.List(ctx, c.Customer, app.Filter{Kind: domain.Kind(in.Kind), Project: in.Project, Item: in.Item, Limit: limit})
	if err != nil {
		return nil, Entries{}, err
	}
	out := Entries{Entries: []EntrySummary{}}
	for _, e := range list {
		out.Entries = append(out.Entries, summary(e, 0))
	}
	return nil, out, nil
}

// GetInput is the input of knowledge_get.
type GetInput struct {
	ID string `json:"id" jsonschema:"entry ID"`
}

func (t tools) get(ctx context.Context, req *mcp.CallToolRequest, in GetInput) (*mcp.CallToolResult, Entry, error) {
	ctx, c, err := scope(ctx, req)
	if err != nil {
		return nil, Entry{}, err
	}
	e, err := t.s.Get(ctx, c.Customer, in.ID)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, full(e), nil
}

// CreateInput is the input of knowledge_create.
type CreateInput struct {
	Kind     string   `json:"kind" jsonschema:"document, decision, note or debt"`
	Title    string   `json:"title"`
	Body     string   `json:"body,omitempty" jsonschema:"Markdown"`
	Projects []string `json:"projects,omitempty" jsonschema:"project keys; defaults to the run's project"`
	Items    []string `json:"items,omitempty" jsonschema:"linked tracker item keys"`
}

func (t tools) create(ctx context.Context, req *mcp.CallToolRequest, in CreateInput) (*mcp.CallToolResult, Entry, error) {
	ctx, c, err := scope(ctx, req)
	if err != nil {
		return nil, Entry{}, err
	}
	projects := in.Projects
	if len(projects) == 0 && c.Project != "" {
		projects = []string{c.Project}
	}
	e, err := t.s.Create(ctx, c.Customer, app.CreateInput{
		Kind: domain.Kind(in.Kind), Title: in.Title, Body: in.Body, Projects: projects, Items: in.Items,
	})
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, full(e), nil
}

// UpdateInput is the input of knowledge_update.
type UpdateInput struct {
	ID       string    `json:"id"`
	Version  int64     `json:"version" jsonschema:"the version you read; stale versions are rejected"`
	Kind     *string   `json:"kind,omitempty"`
	Title    *string   `json:"title,omitempty"`
	Body     *string   `json:"body,omitempty"`
	Projects *[]string `json:"projects,omitempty"`
	Items    *[]string `json:"items,omitempty"`
}

func (t tools) update(ctx context.Context, req *mcp.CallToolRequest, in UpdateInput) (*mcp.CallToolResult, Entry, error) {
	ctx, c, err := scope(ctx, req)
	if err != nil {
		return nil, Entry{}, err
	}
	u := app.UpdateInput{Version: in.Version, Title: in.Title, Body: in.Body, Projects: in.Projects, Items: in.Items}
	if in.Kind != nil {
		k := domain.Kind(*in.Kind)
		u.Kind = &k
	}
	e, err := t.s.Update(ctx, c.Customer, in.ID, u)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, full(e), nil
}
