package process_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/runnerproto"
	"github.com/denyszorinets/ballet/runner/internal/backend/process"
)

type output struct {
	mu     sync.Mutex
	stdout strings.Builder
	stderr strings.Builder
	system strings.Builder
}

func (o *output) write(stream, text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch stream {
	case runnerproto.StreamStdout:
		o.stdout.WriteString(text)
	case runnerproto.StreamStderr:
		o.stderr.WriteString(text)
	default:
		o.system.WriteString(text)
	}
}

func sh(script string, env map[string]string) runnerproto.Spec {
	return runnerproto.Spec{Command: []string{"sh", "-c", script}, Env: env, Workdir: "repo"}
}

func TestProcess_RunsInAFreshWorkspace(t *testing.T) {
	root := t.TempDir()
	b := &process.Backend{WorkRoot: root}
	var o output
	t.Setenv("BALLET_RUNNER_SECRET", "do-not-leak")
	code, err := b.Run(t.Context(), "run-1", sh(`pwd; echo "home=$HOME"; echo "x=$X"; echo "secret=$BALLET_RUNNER_SECRET"; echo oops >&2; touch f; exit 3`,
		map[string]string{"X": "1"}), o.write)
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	lines := strings.Split(strings.TrimSpace(o.stdout.String()), "\n")
	require.Len(t, lines, 4)
	assert.True(t, strings.HasSuffix(lines[0], "/repo"), lines[0])
	assert.Contains(t, lines[1], "/.home")
	assert.Equal(t, "x=1", lines[2])
	assert.Equal(t, "secret=", lines[3], "the Runner's environment is not inherited")
	assert.Equal(t, "oops\n", o.stderr.String())
	assert.Contains(t, o.system.String(), "workspace")
	entries, _ := os.ReadDir(root)
	assert.Empty(t, entries, "the workspace is removed")
}

func TestProcess_CancelStopsTheProcessGroup(t *testing.T) {
	root := t.TempDir()
	b := &process.Backend{WorkRoot: root, StopGrace: 200 * time.Millisecond}
	ctx, cancel := context.WithCancel(t.Context())
	var o output
	done := make(chan error, 1)
	go func() {
		_, err := b.Run(ctx, "run-2", sh(`(sleep 30; echo child) & echo started; wait`, nil), o.write)
		done <- err
	}()
	require.Eventually(t, func() bool { o.mu.Lock(); defer o.mu.Unlock(); return strings.Contains(o.stdout.String(), "started") },
		5*time.Second, 10*time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(start), 5*time.Second, "children are stopped too")
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestProcess_Errors(t *testing.T) {
	b := &process.Backend{WorkRoot: t.TempDir(), Keep: true}
	_, err := b.Run(t.Context(), "run-3", runnerproto.Spec{Command: []string{"/no/such/binary"}}, func(string, string) {})
	assert.ErrorContains(t, err, "start /no/such/binary")

	var o output
	code, err := b.Run(t.Context(), "run-4", sh(`pwd`, nil), o.write)
	require.NoError(t, err)
	assert.Zero(t, code)
	dir := strings.TrimSpace(o.stdout.String())
	_, statErr := os.Stat(dir)
	assert.NoError(t, statErr, "Keep keeps workspaces")
	assert.True(t, strings.HasPrefix(dir, b.WorkRoot), "workdirs stay inside the workspace: %s", dir)

	o = output{}
	_, err = b.Run(t.Context(), "run-5", runnerproto.Spec{Command: []string{"pwd"}, Workdir: "../../etc"}, o.write)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(o.stdout.String()), b.WorkRoot), "no escaping the workspace")
	_ = filepath.Join
}
