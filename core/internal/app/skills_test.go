package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/skill"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

func newSkills(t *testing.T) (*app.Skills, rbacEnv) {
	t.Helper()
	env := newRBACEnv(t)
	seed(t, env)
	return &app.Skills{Store: env.store, Tenancy: env.store, Authz: env.rbac, Now: time.Now, NewID: store.NewID}, env
}

func content(desc, body string) skill.Content {
	return skill.Content{Description: desc, Body: body, Files: map[string]string{"scripts/check.sh": "echo ok"}}
}

func TestSkills_PublishedVersionsAreImmutable(t *testing.T) {
	sk, env := newSkills(t)
	dave := user(t, "dave", "acme-admins")
	s, err := sk.CreateSkill(dave, "customer:acme", "gitflow", content("Branching rules", "# v1"))
	require.NoError(t, err)
	assert.Zero(t, s.LatestVersion)

	v1, err := sk.Publish(dave, s.ID, s.Version)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v1.Number)
	assert.Equal(t, "dave", v1.PublishedBy)

	s, err = sk.GetSkill(dave, s.ID)
	require.NoError(t, err)
	body := "# v2"
	s, err = sk.UpdateDraft(dave, s.ID, app.UpdateDraftInput{Version: s.Version, Body: &body})
	require.NoError(t, err)
	_, err = sk.Publish(dave, s.ID, s.Version)
	require.NoError(t, err)

	old, err := sk.Version(dave, s.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "# v1", old.Content.Body, "editing the draft never changes a published version")
	assert.Equal(t, "echo ok", old.Content.Files["scripts/check.sh"])
	versions, err := sk.Versions(dave, s.ID)
	require.NoError(t, err)
	assert.Equal(t, []int64{2, 1}, []int64{versions[0].Number, versions[1].Number})

	_, err = sk.Publish(dave, s.ID, 1)
	assert.ErrorIs(t, err, app.ErrConflict, "stale version")
	events, err := env.store.ListEvents(t.Context(), store.EventFilter{EntityType: "skill"})
	require.NoError(t, err)
	assert.Len(t, events, 4, "created, published, updated, published")
}

func TestSkills_ScopesAndAuthorization(t *testing.T) {
	sk, _ := newSkills(t)
	alice := user(t, "alice", "ballet-admins")
	dave := user(t, "dave", "acme-admins")
	bob := user(t, "bob", "acme-devs")

	_, err := sk.CreateSkill(alice, "platform", "code-review", content("Review checklist", "# Review"))
	require.NoError(t, err)
	_, err = sk.CreateSkill(dave, "platform", "x-skill", content("x", ""))
	assert.ErrorIs(t, err, app.ErrForbidden, "customer admins cannot change platform process")
	proj, err := sk.CreateSkill(dave, "project:WEB", "code-review", content("Project override", "# WEB review"))
	require.NoError(t, err, "same name at another scope is allowed")
	assert.Equal(t, "acme", proj.Scope.Customer, "project scope records its customer")
	_, err = sk.CreateSkill(dave, "project:GLX", "code-review", content("x", ""))
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = sk.CreateSkill(dave, "project:WEB", "code-review", content("dup", ""))
	assert.ErrorIs(t, err, app.ErrAlreadyExists)

	_, err = sk.GetSkill(bob, proj.ID)
	assert.NoError(t, err, "engineers read skills")
	body := "# hacked"
	_, err = sk.UpdateDraft(bob, proj.ID, app.UpdateDraftInput{Version: 1, Body: &body})
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers do not change process")

	list, err := sk.ListSkills(bob, "project:WEB")
	require.NoError(t, err)
	assert.Len(t, list, 1)
	_, err = sk.ListSkills(user(t, "eve"), "customer:acme")
	assert.ErrorIs(t, err, app.ErrForbidden)

	for name, in := range map[string]struct{ scope, name string }{
		"bad name":  {"platform", "Bad Name"},
		"bad scope": {"team:x", "x-y"},
	} {
		_, err := sk.CreateSkill(alice, in.scope, in.name, content("d", ""))
		assert.ErrorIs(t, err, app.ErrInvalid, name)
	}
	_, err = sk.CreateSkill(alice, "platform", "evil", skill.Content{Description: "d", Files: map[string]string{"../x": ""}})
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = sk.CreateSkill(alice, "customer:nobody", "x-y", content("d", ""))
	assert.ErrorIs(t, err, app.ErrNotFound)
}

func TestSkills_ResolveForProjectWithPins(t *testing.T) {
	sk, _ := newSkills(t)
	alice := user(t, "alice", "ballet-admins")
	dave := user(t, "dave", "acme-admins")
	bob := user(t, "bob", "acme-devs")
	publish := func(ctx context.Context, scope, name, body string, times int) skill.Skill {
		s, err := sk.CreateSkill(ctx, scope, name, content(name, body))
		require.NoError(t, err)
		for range times {
			_, err := sk.Publish(ctx, s.ID, s.Version)
			require.NoError(t, err)
			s, _ = sk.GetSkill(ctx, s.ID)
		}
		return s
	}
	publish(alice, "platform", "code-review", "platform review", 2)
	webReview := publish(dave, "project:WEB", "code-review", "web review", 1)
	publish(alice, "platform", "gitflow", "flow", 3)
	publish(alice, "platform", "unpublished", "x", 0)
	publish(dave, "project:APP", "app-only", "x", 1)

	got, err := sk.Resolve(bob, "WEB")
	require.NoError(t, err)
	names := map[string]skill.Resolved{}
	for _, r := range got {
		names[r.Name] = r
	}
	assert.Len(t, names, 2, "unpublished and other projects' skills are not included")
	assert.Equal(t, webReview.ID, names["code-review"].Skill.ID, "project override wins")
	assert.Equal(t, int64(3), names["gitflow"].Version)

	require.NoError(t, sk.SetPin(dave, "WEB", skill.Pin{Name: "gitflow", Version: 2}))
	got, err = sk.Resolve(bob, "WEB")
	require.NoError(t, err)
	for _, r := range got {
		if r.Name == "gitflow" {
			assert.Equal(t, int64(2), r.Version)
			assert.True(t, r.Pinned)
		}
	}
	require.NoError(t, sk.SetPin(dave, "WEB", skill.Pin{Name: "gitflow", Disabled: true}))
	got, _ = sk.Resolve(bob, "WEB")
	assert.Len(t, got, 1, "disabled skills are excluded")
	pins, err := sk.Pins(bob, "WEB")
	require.NoError(t, err)
	assert.Equal(t, []skill.Pin{{Name: "gitflow", Disabled: true}}, pins, "pins show what resolution hides")
	require.NoError(t, sk.DeletePin(dave, "WEB", "gitflow"))
	got, _ = sk.Resolve(bob, "WEB")
	assert.Len(t, got, 2)
	assert.ErrorIs(t, sk.DeletePin(dave, "WEB", "gitflow"), app.ErrNotFound)

	assert.ErrorIs(t, sk.SetPin(bob, "WEB", skill.Pin{Name: "gitflow", Version: 1}), app.ErrForbidden, "engineers do not pin")
	assert.ErrorIs(t, sk.SetPin(dave, "WEB", skill.Pin{Name: "gitflow", Version: -1}), app.ErrInvalid)
	_, err = sk.Resolve(user(t, "eve"), "WEB")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = sk.Pins(user(t, "eve"), "WEB")
	assert.ErrorIs(t, err, app.ErrForbidden)
}

func TestSkills_ProjectSkillBodyFollowsPins(t *testing.T) {
	sk, _ := newSkills(t)
	alice := user(t, "alice", "ballet-admins")
	dave := user(t, "dave", "acme-admins")
	bob := user(t, "bob", "acme-devs")

	body, err := sk.ProjectSkillBody(bob, "WEB", "planner")
	require.NoError(t, err)
	assert.Empty(t, body, "no planner skill")

	s, err := sk.CreateSkill(alice, "platform", "planner", content("Planning", "v1 rules"))
	require.NoError(t, err)
	_, err = sk.Publish(alice, s.ID, s.Version)
	require.NoError(t, err)
	s, _ = sk.GetSkill(alice, s.ID)
	_, err = sk.UpdateDraft(alice, s.ID, app.UpdateDraftInput{Version: s.Version, Body: ptr("v2 rules")})
	require.NoError(t, err)
	s, _ = sk.GetSkill(alice, s.ID)
	_, err = sk.Publish(alice, s.ID, s.Version)
	require.NoError(t, err)

	body, err = sk.ProjectSkillBody(bob, "WEB", "planner")
	require.NoError(t, err)
	assert.Equal(t, "v2 rules", body)
	require.NoError(t, sk.SetPin(dave, "WEB", skill.Pin{Name: "planner", Version: 1}))
	body, _ = sk.ProjectSkillBody(bob, "WEB", "planner")
	assert.Equal(t, "v1 rules", body)
}
