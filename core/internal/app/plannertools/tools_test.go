package plannertools_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/app/plannertools"
	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/domain/skill"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/kit/auth"
)

func user(t *testing.T, subject string, groups ...string) context.Context {
	items := make([]any, len(groups))
	for i, g := range groups {
		items[i] = g
	}
	return auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: subject, Claims: map[string]any{"groups": items}})
}

// fakeKnowledge records calls and answers with a fixed body.
type fakeKnowledge struct {
	calls []string
	body  any
}

func (f *fakeKnowledge) Do(ctx context.Context, organization string, write bool, method, path string, q url.Values, body any) (json.RawMessage, error) {
	if _, ok := app.PlannerSessionOf(ctx); !ok {
		return nil, app.ErrForbidden
	}
	f.calls = append(f.calls, method+" "+organization+"/"+path+"?"+q.Encode())
	f.body = body
	return json.RawMessage(`{"ok":true}`), nil
}

type env struct {
	tools   map[string]app.PlannerTool
	tracker *app.Tracker
	cs      *app.Changesets
	skills  *app.Skills
	kn      *fakeKnowledge
}

func setup(t *testing.T) env {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir()+"/core.db")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	boot, _ := rbac.ParseBootstrap("groups:admins")
	r := &app.RBAC{Store: st, Bootstrap: []rbac.Binding{boot}}
	ten := &app.Tenancy{Store: st, Authz: r, Now: time.Now, NewID: store.NewID}
	alice := user(t, "alice", "admins")
	_, err = ten.CreateOrganization(alice, app.CreateOrganizationInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	_, err = ten.CreateProject(alice, app.CreateProjectInput{OrganizationKey: "acme", Key: "WEB", Name: "Web"})
	require.NoError(t, err)
	bindings := &app.RoleBindings{RBAC: r, Tenancy: st, Now: time.Now, NewID: store.NewID}
	for _, b := range []app.CreateRoleBindingInput{
		{Claim: "groups", Value: "devs", Role: "engineer", Scope: "organization:acme"},
		{Claim: "groups", Value: "viewers", Role: "viewer", Scope: "organization:acme"},
	} {
		_, err := bindings.Create(alice, b)
		require.NoError(t, err)
	}
	tr := &app.Tracker{Items: st, Deps: st, Tenancy: st, Events: st, Authz: r, Now: time.Now, NewID: store.NewID}
	e := env{
		tracker: tr, cs: &app.Changesets{Store: st, Tracker: tr},
		skills: &app.Skills{Store: st, Tenancy: st, Authz: r, Now: time.Now, NewID: store.NewID},
		kn:     &fakeKnowledge{}, tools: map[string]app.PlannerTool{},
	}
	for _, tl := range plannertools.All(plannertools.Deps{
		Tracker: tr, Changesets: e.cs, Skills: e.skills, Knowledge: e.kn,
		Search: &app.Search{Store: st, Tenancy: st, Authz: r},
	}) {
		e.tools[tl.Spec().Name] = tl
	}
	return e
}

var webEnv = app.ToolEnv{OrganizationKey: "acme", ProjectKey: "WEB", SessionID: "s1"}

func (e env) call(t *testing.T, ctx context.Context, name, input string) (string, error) {
	t.Helper()
	tl, ok := e.tools[name]
	require.True(t, ok, name)
	return tl.Call(app.ActingAsPlanner(ctx, "s1"), webEnv, json.RawMessage(input))
}

func TestTools_SchemasAreValidJSON(t *testing.T) {
	e := setup(t)
	assert.Len(t, e.tools, 12)
	for name, tl := range e.tools {
		var schema map[string]any
		require.NoError(t, json.Unmarshal(tl.Spec().InputSchema, &schema), name)
		assert.Equal(t, "object", schema["type"], name)
		assert.NotEmpty(t, tl.Spec().Description, name)
	}
}

func TestTools_TrackerReads(t *testing.T) {
	e := setup(t)
	bob := user(t, "bob", "devs")
	a, err := e.tracker.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "Schema",
		AcceptanceCriteria: []string{"migrates"}})
	require.NoError(t, err)
	b, err := e.tracker.CreateItem(bob, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: "API"})
	require.NoError(t, err)
	_, err = e.tracker.AddDependency(bob, a.Key, app.DirBlocks, b.Key)
	require.NoError(t, err)

	out, err := e.call(t, bob, "list_items", `{"kind":"ticket"}`)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"key":"WEB-1","kind":"ticket","title":"Schema","state":"backlog","type":"feature"},
		{"key":"WEB-2","kind":"ticket","title":"API","state":"backlog","type":"feature"}]`, out)

	out, err = e.call(t, bob, "get_item", `{"key":"WEB-2"}`)
	require.NoError(t, err)
	var item map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &item))
	assert.Equal(t, "blocked_by", item["dependencies"].([]any)[0].(map[string]any)["type"])

	out, err = e.call(t, bob, "list_runnable", `{}`)
	require.NoError(t, err)
	assert.Equal(t, "[]", out)

	_, err = e.call(t, bob, "get_item", `{"key":"WEB-1","extra":1}`)
	assert.ErrorIs(t, err, app.ErrInvalid, "unknown input fields are rejected")
	_, err = e.call(t, bob, "get_item", `{"key":"WEB-99"}`)
	assert.ErrorIs(t, err, app.ErrNotFound)
}

func TestTools_ProposeChangesetOnly(t *testing.T) {
	e := setup(t)
	bob := user(t, "bob", "devs")
	out, err := e.call(t, bob, "propose_changeset", `{"title":"Auth","summary":"why","operations":[
		{"kind":"create_item","ref":"auth","create":{"kind":"epic","title":"Auth"}},
		{"kind":"create_item","ref":"login","create":{"kind":"ticket","title":"Login","epic":"$auth"}}]}`)
	require.NoError(t, err)
	var res map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.Equal(t, "proposed", res["status"])

	items, _ := e.tracker.ListItems(bob, "WEB", "", "", "", "")
	assert.Empty(t, items, "proposing creates nothing")
	cs, err := e.cs.Get(bob, res["changeset"].(string))
	require.NoError(t, err)
	assert.Equal(t, "planner:s1", cs.ProposedBy.Subject)

	out, err = e.call(t, bob, "list_changesets", `{"status":"proposed"}`)
	require.NoError(t, err)
	assert.Contains(t, out, `"operations":2`)

	_, err = e.call(t, bob, "propose_changeset", `{"title":"x","operations":[{"kind":"create_item","ref":"x",
		"create":{"kind":"ticket","title":"x","policy":{"review_mode":"agent","merge_mode":"auto"}}}]}`)
	assert.ErrorIs(t, err, app.ErrInvalid, "policies are for humans")
	_, err = e.call(t, user(t, "carol", "viewers"), "propose_changeset",
		`{"title":"x","operations":[{"kind":"create_item","ref":"x","create":{"kind":"ticket","title":"x"}}]}`)
	assert.ErrorIs(t, err, app.ErrForbidden, "the planner has the human's permissions")
	_ = changeset.StatusProposed
}

func TestTools_SkillsAndKnowledge(t *testing.T) {
	e := setup(t)
	alice := user(t, "alice", "admins")
	bob := user(t, "bob", "devs")
	s, err := e.skills.CreateSkill(alice, "platform", "gitflow", skill.Content{Description: "Branching", Body: "Use develop."})
	require.NoError(t, err)
	_, err = e.skills.Publish(alice, s.ID, s.Version)
	require.NoError(t, err)

	out, err := e.call(t, bob, "list_skills", `{}`)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"name":"gitflow","scope":"platform","version":1}]`, out)
	out, err = e.call(t, bob, "read_skill", `{"name":"gitflow"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "Use develop.")
	_, err = e.call(t, bob, "read_skill", `{"name":"nope"}`)
	assert.ErrorIs(t, err, app.ErrNotFound)

	_, err = e.call(t, bob, "search_knowledge", `{"query":"auth","kind":"decision"}`)
	require.NoError(t, err)
	_, err = e.call(t, bob, "create_knowledge", `{"kind":"note","title":"T","body":"B","items":["WEB-1"]}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"WEB"}, e.kn.body.(map[string]any)["projects"])
	_, err = e.call(t, bob, "update_knowledge", `{"id":"k1","version":2,"title":"T2"}`)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"version": int64(2), "title": "T2"}, e.kn.body)
	_, err = e.call(t, bob, "get_knowledge", `{"id":"k/1"}`)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"GET acme/search?kind=decision&q=auth", "POST acme/entries?", "PATCH acme/entries/k1?", "GET acme/entries/k%2F1?",
	}, e.kn.calls)

	_, err = e.call(t, bob, "search_project", `{"query":"anything"}`)
	require.NoError(t, err)
}
