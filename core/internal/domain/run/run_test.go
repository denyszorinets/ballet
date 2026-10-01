package run_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/core/internal/domain/run"
)

func TestTransitions(t *testing.T) {
	ok := [][2]run.Status{
		{run.StatusQueued, run.StatusStarting}, {run.StatusQueued, run.StatusCancelled},
		{run.StatusStarting, run.StatusRunning}, {run.StatusStarting, run.StatusFailed},
		{run.StatusStarting, run.StatusSucceeded}, {run.StatusRunning, run.StatusSucceeded},
		{run.StatusRunning, run.StatusFailed}, {run.StatusRunning, run.StatusCancelled},
		{run.StatusStarting, run.StatusQueued},
	}
	for _, p := range ok {
		assert.True(t, run.CanTransition(p[0], p[1]), "%s → %s", p[0], p[1])
	}
	bad := [][2]run.Status{
		{run.StatusQueued, run.StatusRunning}, {run.StatusSucceeded, run.StatusFailed},
		{run.StatusFailed, run.StatusQueued}, {run.StatusCancelled, run.StatusRunning},
		{run.StatusRunning, run.StatusQueued},
	}
	for _, p := range bad {
		assert.False(t, run.CanTransition(p[0], p[1]), "%s → %s", p[0], p[1])
	}
	assert.True(t, run.StatusFailed.Terminal())
	assert.False(t, run.StatusRunning.Terminal())
	assert.True(t, run.StatusRunning.Active())
	assert.False(t, run.StatusQueued.Active())
}

func TestValidate(t *testing.T) {
	r := run.Run{Stage: "implement", Spec: run.Spec{Command: []string{"make", "test"}}}
	assert.NoError(t, r.Validate())
	r.Spec.Command = nil
	assert.Error(t, r.Validate())
	r = run.Run{Stage: "", Spec: run.Spec{Command: []string{"x"}}}
	assert.Error(t, r.Validate())
	r = run.Run{Stage: "implement", Spec: run.Spec{Command: []string{"x"}, Env: map[string]string{"bad key": "v"}}}
	assert.Error(t, r.Validate())
}
