package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
)

func TestPipelines_TemplateVersionsAndTicketTypes(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	ps := &app.Pipelines{Store: env.store, Tenancy: env.store, Authz: env.rbac, Adapters: []string{"claude-code"}, Now: time.Now}
	dave := user(t, "dave", "acme-admins")
	bob := user(t, "bob", "acme-devs")

	list, err := ps.List(bob, "WEB")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int64(0), list[0].Version, "Ballet's template until the project saves one")
	got, err := ps.Get(bob, "WEB", "default", 0)
	require.NoError(t, err)
	assert.Equal(t, pipeline.Default(), got.Definition)

	custom := pipeline.Default()
	custom.MaxIterations = 5
	_, err = ps.Save(bob, "WEB", "default", custom, 0)
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers do not change the process")
	v1, err := ps.Save(dave, "WEB", "default", custom, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v1.Version)
	_, err = ps.Save(dave, "WEB", "default", custom, 0)
	assert.ErrorIs(t, err, app.ErrConflict, "saved after a stale version")
	bad := custom
	bad.Stages = nil
	_, err = ps.Save(dave, "WEB", "default", bad, 1)
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = ps.Save(dave, "WEB", "Bad Name", custom, 0)
	assert.ErrorIs(t, err, app.ErrInvalid)

	bugs := pipeline.Definition{MaxIterations: 2, Stages: []pipeline.Stage{
		{ID: "fix", Kind: pipeline.KindAgent}, {ID: "integrate", Kind: pipeline.KindPlatform, Action: pipeline.ActionMerge}}}
	_, err = ps.Save(dave, "WEB", "bug", bugs, 0)
	require.NoError(t, err)

	p, err := env.store.ProjectByKey(t.Context(), "WEB")
	require.NoError(t, err)
	forBug, err := ps.ForTicket(t.Context(), p.ID, "bug")
	require.NoError(t, err)
	assert.Equal(t, "bug", forBug.Name)
	forFeature, err := ps.ForTicket(t.Context(), p.ID, "feature")
	require.NoError(t, err)
	assert.Equal(t, "default", forFeature.Name)
	assert.Equal(t, 5, forFeature.Definition.MaxIterations)

	list, err = ps.List(bob, "WEB")
	require.NoError(t, err)
	assert.Len(t, list, 2)
	versions, err := ps.Versions(bob, "WEB", "default")
	require.NoError(t, err)
	assert.Len(t, versions, 1)
	_, err = ps.Get(bob, "WEB", "bug", 7)
	assert.ErrorIs(t, err, app.ErrNotFound)
}
