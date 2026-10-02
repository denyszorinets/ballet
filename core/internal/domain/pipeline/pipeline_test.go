package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
)

var adapters = []string{"claude-code"}

func TestDefault(t *testing.T) {
	d := pipeline.Default()
	require.NoError(t, d.Validate(adapters))
	assert.Equal(t, "review", d.Target("implement", pipeline.OutcomeDone))
	assert.Equal(t, "implement", d.Target("review", pipeline.OutcomeFailed))
	assert.Equal(t, pipeline.TargetDone, d.Target("integrate", pipeline.OutcomeDone))
	assert.Equal(t, pipeline.TargetQuestion, d.Target("verify", pipeline.OutcomeBlocked))
	assert.True(t, d.IsLoop("review", "implement"))
	assert.False(t, d.IsLoop("implement", "review"))
}

func TestTargetDefaults(t *testing.T) {
	d := pipeline.Definition{MaxIterations: 1, Stages: []pipeline.Stage{
		{ID: "a", Kind: pipeline.KindAgent}, {ID: "b", Kind: pipeline.KindHuman}}}
	require.NoError(t, d.Validate(adapters))
	assert.Equal(t, "b", d.Target("a", pipeline.OutcomeDone))
	assert.Equal(t, pipeline.TargetDone, d.Target("b", pipeline.OutcomeDone))
	assert.Equal(t, pipeline.TargetQuestion, d.Target("a", pipeline.OutcomeFailed))
}

func TestValidate(t *testing.T) {
	cases := map[string]func(*pipeline.Definition){
		"no stages":       func(d *pipeline.Definition) { d.Stages = nil },
		"iterations":      func(d *pipeline.Definition) { d.MaxIterations = 0 },
		"bad id":          func(d *pipeline.Definition) { d.Stages[0].ID = "Implement" },
		"duplicate":       func(d *pipeline.Definition) { d.Stages[1].ID = "implement" },
		"unknown kind":    func(d *pipeline.Definition) { d.Stages[0].Kind = "robot" },
		"unknown adapter": func(d *pipeline.Definition) { d.Stages[0].Adapter = "codex" },
		"bad action":      func(d *pipeline.Definition) { d.Stages[3].Action = "deploy" },
		"agent action":    func(d *pipeline.Definition) { d.Stages[0].Action = "merge" },
		"unknown target":  func(d *pipeline.Definition) { d.Stages[0].Next[pipeline.OutcomeDone] = "qa" },
		"unknown outcome": func(d *pipeline.Definition) { d.Stages[0].Next["maybe"] = "review" },
		"unreachable": func(d *pipeline.Definition) {
			d.Stages[0].Next[pipeline.OutcomeDone] = "verify"
		},
		"never completes": func(d *pipeline.Definition) {
			d.Stages[3].Next[pipeline.OutcomeDone] = "implement"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := pipeline.Default()
			mutate(&d)
			assert.Error(t, d.Validate(adapters))
		})
	}
}

func TestYAMLRoundTrip(t *testing.T) {
	src, err := pipeline.Default().YAML()
	require.NoError(t, err)
	assert.Contains(t, src, "id: implement")
	d, err := pipeline.ParseYAML(src)
	require.NoError(t, err)
	assert.Equal(t, pipeline.Default(), d)

	_, err = pipeline.ParseYAML("stages: []\nmax_iterations: 2\ncolour: red\n")
	assert.ErrorContains(t, err, "colour")
	_, err = pipeline.ParseYAML("stages: [")
	assert.Error(t, err)
}
