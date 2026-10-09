// Package plannertools defines the planner's tools (ADR-0020): in-process
// calls of Core's use cases and of the Knowledge service. Tools run with
// the turn's context, so they are authorized as the human the planner acts
// for; the planner changes the plan only by proposing changesets.
package plannertools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// Knowledge calls the Knowledge service for the caller in ctx (see
// infra/knowledge.Client): path is relative to the organization's knowledge.
type Knowledge interface {
	Do(ctx context.Context, organization string, write bool, method, path string, query url.Values, body any) (json.RawMessage, error)
}

// Deps are the use cases the tools call.
type Deps struct {
	Tracker    *app.Tracker
	Changesets *app.Changesets
	Skills     *app.Skills
	Search     *app.Search // nil: no search_project tool
	Knowledge  Knowledge   // nil: no knowledge tools
}

// tool adapts a typed function to app.PlannerTool.
type tool[In any] struct {
	spec app.ToolSpec
	fn   func(ctx context.Context, env app.ToolEnv, in In) (any, error)
}

func (t tool[In]) Spec() app.ToolSpec { return t.spec }

func (t tool[In]) Call(ctx context.Context, env app.ToolEnv, input json.RawMessage) (string, error) {
	var in In
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return "", fmt.Errorf("%w: bad input: %v", app.ErrInvalid, err)
	}
	out, err := t.fn(ctx, env, in)
	if err != nil {
		return "", err
	}
	if raw, ok := out.(json.RawMessage); ok {
		return string(raw), nil
	}
	data, err := json.Marshal(out)
	return string(data), err
}

func newTool[In any](name, description, schema string, fn func(context.Context, app.ToolEnv, In) (any, error)) app.PlannerTool {
	return tool[In]{spec: app.ToolSpec{Name: name, Description: description, InputSchema: json.RawMessage(schema)}, fn: fn}
}

// All returns the planner's tools.
func All(d Deps) []app.PlannerTool {
	tools := []app.PlannerTool{
		listItems(d), getItem(d), listRunnable(d),
		listChangesets(d), proposeChangeset(d),
		listSkills(d), readSkill(d),
	}
	if d.Search != nil {
		tools = append(tools, searchProject(d))
	}
	if d.Knowledge != nil {
		tools = append(tools, searchKnowledge(d), getKnowledge(d), createKnowledge(d), updateKnowledge(d))
	}
	return tools
}

// itemSummary is an item as tools show it.
type itemSummary struct {
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Type      string `json:"type,omitempty"`
	Epic      string `json:"epic,omitempty"`
	Milestone string `json:"milestone,omitempty"`
}

func summary(v app.ItemView) itemSummary {
	return itemSummary{Key: v.Key, Kind: string(v.Kind), Title: v.Title, State: string(v.State), Type: string(v.Type),
		Epic: v.EpicKey, Milestone: v.MilestoneKey}
}

func listItems(d Deps) app.PlannerTool {
	type in struct {
		Kind      tracker.Kind  `json:"kind"`
		State     tracker.State `json:"state"`
		Epic      string        `json:"epic"`
		Milestone string        `json:"milestone"`
	}
	return newTool("list_items",
		"List the project's milestones, epics and tickets (key, kind, title, state, epic, milestone). All filters are optional.",
		`{"type":"object","properties":{
			"kind":{"type":"string","enum":["milestone","epic","ticket"]},
			"state":{"type":"string","description":"e.g. backlog, ready, in_progress, done"},
			"epic":{"type":"string","description":"epic key"},
			"milestone":{"type":"string","description":"milestone key"}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			items, err := d.Tracker.ListItems(ctx, env.ProjectKey, in.Kind, in.State, in.Epic, in.Milestone)
			if err != nil {
				return nil, err
			}
			out := make([]itemSummary, 0, len(items))
			for _, it := range items {
				out = append(out, summary(it))
			}
			return out, nil
		})
}

func getItem(d Deps) app.PlannerTool {
	type in struct {
		Key string `json:"key"`
	}
	type dep struct {
		Type  string `json:"type"`
		Key   string `json:"key"`
		Kind  string `json:"kind"`
		Title string `json:"title"`
		State string `json:"state"`
	}
	type out struct {
		itemSummary
		Description        string          `json:"description"`
		AcceptanceCriteria []string        `json:"acceptance_criteria,omitempty"`
		Policy             *tracker.Policy `json:"policy,omitempty"`
		Version            int64           `json:"version"`
		Dependencies       []dep           `json:"dependencies"`
	}
	return newTool("get_item",
		"Get one item with its description, acceptance criteria, policy and dependencies (blocks, blocked_by, relates).",
		`{"type":"object","required":["key"],"properties":{"key":{"type":"string","description":"item key, e.g. WEB-12"}}}`,
		func(ctx context.Context, _ app.ToolEnv, in in) (any, error) {
			it, err := d.Tracker.GetItem(ctx, in.Key)
			if err != nil {
				return nil, err
			}
			deps, err := d.Tracker.Dependencies(ctx, in.Key)
			if err != nil {
				return nil, err
			}
			o := out{itemSummary: summary(it), Description: it.Description, AcceptanceCriteria: it.AcceptanceCriteria,
				Version: it.Version, Dependencies: []dep{}}
			if it.Kind == tracker.KindTicket {
				o.Policy = &it.Policy
			}
			for _, x := range deps {
				o.Dependencies = append(o.Dependencies, dep{Type: string(x.Direction), Key: x.Other.Key, Kind: string(x.Other.Kind),
					Title: x.Other.Title, State: string(x.Other.State)})
			}
			return o, nil
		})
}

func listRunnable(d Deps) app.PlannerTool {
	return newTool("list_runnable",
		"List ready tickets whose blockers are all resolved: what agents may work on now.",
		`{"type":"object","properties":{}}`,
		func(ctx context.Context, env app.ToolEnv, _ struct{}) (any, error) {
			items, err := d.Tracker.Runnable(ctx, env.ProjectKey)
			if err != nil {
				return nil, err
			}
			out := make([]itemSummary, 0, len(items))
			for _, it := range items {
				out = append(out, summary(it))
			}
			return out, nil
		})
}

func listChangesets(d Deps) app.PlannerTool {
	type in struct {
		Status changeset.Status `json:"status"`
	}
	type out struct {
		ID         string           `json:"id"`
		Title      string           `json:"title"`
		Status     changeset.Status `json:"status"`
		Operations int              `json:"operations"`
		Approved   []int            `json:"approved,omitempty"`
	}
	return newTool("list_changesets",
		"List the project's plan changesets (newest first) to see what was proposed, applied or rejected.",
		`{"type":"object","properties":{"status":{"type":"string","enum":["proposed","applied","rejected"]}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			list, err := d.Changesets.List(ctx, env.ProjectKey, in.Status)
			if err != nil {
				return nil, err
			}
			res := make([]out, 0, len(list))
			for _, c := range list {
				res = append(res, out{ID: c.ID, Title: c.Title, Status: c.Status, Operations: len(c.Ops), Approved: c.Approved})
			}
			return res, nil
		})
}

// changesetSchema describes propose_changeset's input to the model.
const changesetSchema = `{"type":"object","required":["title","operations"],"properties":{
	"title":{"type":"string","description":"short name of the change"},
	"summary":{"type":"string","description":"Markdown: why these changes"},
	"operations":{"type":"array","items":{"type":"object","required":["kind"],"properties":{
		"kind":{"type":"string","enum":["create_item","update_item","add_dependency"]},
		"ref":{"type":"string","description":"create_item: a name (lowercase, digits, - or _) later operations use as \"$name\""},
		"create":{"type":"object","required":["kind","title"],"properties":{
			"kind":{"type":"string","enum":["milestone","epic","ticket"]},
			"title":{"type":"string"},"description":{"type":"string"},
			"type":{"type":"string","enum":["feature","bug","tech_debt","docs","spike"]},
			"acceptance_criteria":{"type":"array","items":{"type":"string"}},
			"epic":{"type":"string","description":"tickets: epic key or $ref"},
			"milestone":{"type":"string","description":"tickets and epics: milestone key or $ref"}}},
		"update":{"type":"object","required":["item"],"properties":{
			"item":{"type":"string","description":"key of an existing item"},
			"title":{"type":"string"},"description":{"type":"string"},
			"type":{"type":"string","enum":["feature","bug","tech_debt","docs","spike"]},
			"acceptance_criteria":{"type":"array","items":{"type":"string"}},
			"epic":{"type":"string"},"milestone":{"type":"string"}}},
		"dependency":{"type":"object","required":["from","to","type"],"properties":{
			"from":{"type":"string","description":"item key or $ref"},
			"to":{"type":"string","description":"item key or $ref"},
			"type":{"type":"string","enum":["blocks","relates"],"description":"blocks: from must be done before to starts"}}}}}}}}`

func proposeChangeset(d Deps) app.PlannerTool {
	type in struct {
		Title      string         `json:"title"`
		Summary    string         `json:"summary"`
		Operations []changeset.Op `json:"operations"`
	}
	return newTool("propose_changeset",
		"Propose planning changes (create items, update items, add dependencies) as one changeset. Nothing changes "+
			"until the human approves it, wholly or in part, in the chat. Each operation sets exactly the payload of "+
			"its kind (create, update or dependency). Refer to items created in the same changeset as \"$ref\" (declared "+
			"by an earlier create_item). Execution policies are set by humans; do not set them.",
		changesetSchema,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			for _, op := range in.Operations {
				if (op.Create != nil && op.Create.Policy != nil) || (op.Update != nil && op.Update.Policy != nil) {
					return nil, fmt.Errorf("%w: only humans set execution policies", app.ErrInvalid)
				}
			}
			v, err := d.Changesets.Propose(ctx, app.ProposeInput{
				ProjectKey: env.ProjectKey, Title: in.Title, Summary: in.Summary, Ops: in.Operations,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"changeset": v.ID, "status": v.Status, "operations": len(v.Ops),
				"note": "Proposed. The human reviews and approves it in the chat; do not assume it is applied."}, nil
		})
}

func listSkills(d Deps) app.PlannerTool {
	type out struct {
		Name    string `json:"name"`
		Scope   string `json:"scope,omitempty"`
		Version int64  `json:"version"`
		Problem string `json:"problem,omitempty"`
	}
	return newTool("list_skills",
		"List the skills the project's agents use (name, scope, version): its engineering process.",
		`{"type":"object","properties":{}}`,
		func(ctx context.Context, env app.ToolEnv, _ struct{}) (any, error) {
			resolved, err := d.Skills.Resolve(ctx, env.ProjectKey)
			if err != nil {
				return nil, err
			}
			res := make([]out, 0, len(resolved))
			for _, r := range resolved {
				o := out{Name: r.Name, Version: r.Version, Problem: r.Problem}
				if r.Skill.ID != "" {
					o.Scope = r.Skill.Scope.String()
				}
				res = append(res, o)
			}
			return res, nil
		})
}

func readSkill(d Deps) app.PlannerTool {
	type in struct {
		Name string `json:"name"`
	}
	return newTool("read_skill",
		"Read the SKILL.md of a skill in the version the project uses.",
		`{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			body, err := d.Skills.ProjectSkillBody(ctx, env.ProjectKey, in.Name)
			if err != nil {
				return nil, err
			}
			if body == "" {
				return nil, fmt.Errorf("%w: the project uses no skill %q", app.ErrNotFound, in.Name)
			}
			return map[string]string{"name": in.Name, "body": body}, nil
		})
}

func searchProject(d Deps) app.PlannerTool {
	type in struct {
		Query string `json:"query"`
		Kind  string `json:"kind"`
	}
	type hit struct {
		Kind  string `json:"kind"`
		Ref   string `json:"ref"`
		Title string `json:"title"`
	}
	return newTool("search_project",
		"Hybrid full-text and semantic search over the project's items and skills.",
		`{"type":"object","required":["query"],"properties":{"query":{"type":"string"},"kind":{"type":"string","enum":["item","skill"]}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			hits, err := d.Search.Query(ctx, in.Query, in.Kind, env.ProjectKey, 20)
			if err != nil {
				return nil, err
			}
			out := make([]hit, 0, len(hits))
			for _, h := range hits {
				out = append(out, hit{Kind: h.Kind, Ref: h.Ref, Title: h.Title})
			}
			return out, nil
		})
}

func searchKnowledge(d Deps) app.PlannerTool {
	type in struct {
		Query string `json:"query"`
		Kind  string `json:"kind"`
		Limit int    `json:"limit"`
	}
	return newTool("search_knowledge",
		"Search the organization's knowledge base (documents, decisions, notes, debt), hybrid full-text and semantic.",
		`{"type":"object","required":["query"],"properties":{"query":{"type":"string"},
			"kind":{"type":"string","enum":["document","decision","note","debt"]},
			"limit":{"type":"integer","minimum":1,"maximum":50}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			q := url.Values{"q": {in.Query}}
			if in.Kind != "" {
				q.Set("kind", in.Kind)
			}
			if in.Limit > 0 {
				q.Set("limit", strconv.Itoa(in.Limit))
			}
			return d.Knowledge.Do(ctx, env.OrganizationKey, false, http.MethodGet, "search", q, nil)
		})
}

func getKnowledge(d Deps) app.PlannerTool {
	type in struct {
		ID string `json:"id"`
	}
	return newTool("get_knowledge",
		"Read a knowledge entry (Markdown body, linked projects and items, version).",
		`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			return d.Knowledge.Do(ctx, env.OrganizationKey, false, http.MethodGet, "entries/"+url.PathEscape(in.ID), nil, nil)
		})
}

func createKnowledge(d Deps) app.PlannerTool {
	type in struct {
		Kind  string   `json:"kind"`
		Title string   `json:"title"`
		Body  string   `json:"body"`
		Items []string `json:"items"`
	}
	return newTool("create_knowledge",
		"Write a new knowledge entry for this project: a document, decision (with rationale), note or debt record. "+
			"Link it to tracker items by key.",
		`{"type":"object","required":["kind","title","body"],"properties":{
			"kind":{"type":"string","enum":["document","decision","note","debt"]},
			"title":{"type":"string"},"body":{"type":"string","description":"Markdown"},
			"items":{"type":"array","items":{"type":"string"},"description":"linked item keys"}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			return d.Knowledge.Do(ctx, env.OrganizationKey, true, http.MethodPost, "entries", nil, map[string]any{
				"kind": in.Kind, "title": in.Title, "body": in.Body, "projects": []string{env.ProjectKey}, "items": in.Items,
			})
		})
}

func updateKnowledge(d Deps) app.PlannerTool {
	type in struct {
		ID      string    `json:"id"`
		Version int64     `json:"version"`
		Kind    *string   `json:"kind"`
		Title   *string   `json:"title"`
		Body    *string   `json:"body"`
		Items   *[]string `json:"items"`
	}
	return newTool("update_knowledge",
		"Update a knowledge entry; send the version you read (get_knowledge) and only the fields to change.",
		`{"type":"object","required":["id","version"],"properties":{
			"id":{"type":"string"},"version":{"type":"integer"},
			"kind":{"type":"string","enum":["document","decision","note","debt"]},
			"title":{"type":"string"},"body":{"type":"string"},
			"items":{"type":"array","items":{"type":"string"}}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			body := map[string]any{"version": in.Version}
			if in.Kind != nil {
				body["kind"] = *in.Kind
			}
			if in.Title != nil {
				body["title"] = *in.Title
			}
			if in.Body != nil {
				body["body"] = *in.Body
			}
			if in.Items != nil {
				body["items"] = *in.Items
			}
			return d.Knowledge.Do(ctx, env.OrganizationKey, true, http.MethodPatch, "entries/"+url.PathEscape(in.ID), nil, body)
		})
}
