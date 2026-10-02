package docker_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/runnerproto"
	"github.com/denyszorinets/ballet/runner/internal/backend/docker"
)

// engine is a fake Engine API with one container at a time.
type engine struct {
	mu      sync.Mutex
	calls   []string
	created map[string]any
	removed bool
	pulled  string
	stop    chan struct{}
	slow    bool
}

func frame(stream byte, text string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(text)))
	return append(h, text...)
}

func (e *engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/"+docker.APIVersion)
	e.mu.Lock()
	e.calls = append(e.calls, r.Method+" "+p)
	e.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/images/"):
		if strings.Contains(p, "missing") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such image"}`))
		}
	case p == "/images/create":
		e.mu.Lock()
		e.pulled = r.URL.Query().Get("fromImage") + ":" + r.URL.Query().Get("tag")
		e.mu.Unlock()
		if strings.Contains(r.URL.Query().Get("fromImage"), "denied") {
			_, _ = w.Write([]byte("{\"status\":\"Pulling\"}\n{\"error\":\"access denied\"}\n"))
			return
		}
		_, _ = w.Write([]byte("{\"status\":\"Pulling\"}\n{\"status\":\"Done\"}\n"))
	case p == "/containers/create":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.created = body
		e.created["name"] = r.URL.Query().Get("name")
		e.mu.Unlock()
		_, _ = w.Write([]byte(`{"Id":"c0ffee1234567890"}`))
	case strings.HasSuffix(p, "/start"):
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(p, "/logs"):
		w.(http.Flusher).Flush()
		_, _ = w.Write(frame(1, "hello\n"))
		_, _ = w.Write(frame(2, "warn\n"))
		w.(http.Flusher).Flush()
		if e.slow {
			<-e.stop
		}
	case strings.HasSuffix(p, "/wait"):
		if e.slow {
			<-e.stop
			_, _ = w.Write([]byte(`{"StatusCode":137}`))
			return
		}
		_, _ = w.Write([]byte(`{"StatusCode":3}`))
	case strings.HasSuffix(p, "/stop"):
		close(e.stop)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete:
		e.mu.Lock()
		e.removed = r.URL.Query().Get("force") == "1"
		e.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func setup(t *testing.T, slow bool) (*engine, *docker.Backend) {
	t.Helper()
	e := &engine{stop: make(chan struct{}), slow: slow}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	b, err := docker.New(srv.URL)
	require.NoError(t, err)
	b.NanoCPUs, b.MemoryBytes, b.StopGrace = 2e9, 1<<30, time.Second
	return e, b
}

type output struct {
	mu   sync.Mutex
	text map[string]string
}

func (o *output) write(stream, text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.text == nil {
		o.text = map[string]string{}
	}
	o.text[stream] += text
}

func TestDocker_RunsAContainer(t *testing.T) {
	e, b := setup(t, false)
	var o output
	code, err := b.Run(t.Context(), "run-1", runnerproto.Spec{Image: "missing/img:2", Command: []string{"make", "test"},
		Env: map[string]string{"A": "1"}, Workdir: "repo"}, o.write)
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, "hello\n", o.text["stdout"])
	assert.Equal(t, "warn\n", o.text["stderr"])
	assert.Contains(t, o.text["system"], "pulling missing/img:2")
	assert.Equal(t, "missing/img:2", e.pulled)

	assert.Equal(t, "ballet-run-run-1", e.created["name"])
	assert.Equal(t, []any{"make", "test"}, e.created["Cmd"])
	assert.Equal(t, []any{"A=1"}, e.created["Env"])
	assert.Equal(t, "/workspace/repo", e.created["WorkingDir"])
	assert.Equal(t, map[string]any{"ballet.run": "run-1"}, e.created["Labels"])
	host := e.created["HostConfig"].(map[string]any)
	assert.Equal(t, 2e9, host["NanoCpus"])
	assert.Equal(t, true, host["Init"])
	assert.True(t, e.removed, "the container is removed")
}

func TestDocker_CancelStopsTheContainer(t *testing.T) {
	e, b := setup(t, true)
	ctx, cancel := context.WithCancel(t.Context())
	var o output
	done := make(chan error, 1)
	go func() {
		_, err := b.Run(ctx, "run-2", runnerproto.Spec{Image: "img", Command: []string{"sleep", "60"}}, o.write)
		done <- err
	}()
	require.Eventually(t, func() bool { o.mu.Lock(); defer o.mu.Unlock(); return o.text["stdout"] != "" }, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("not stopped")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	assert.Contains(t, e.calls, "POST /containers/c0ffee1234567890/stop")
	assert.True(t, e.removed)
}

func TestDocker_Errors(t *testing.T) {
	_, b := setup(t, false)
	_, err := b.Run(t.Context(), "r", runnerproto.Spec{Command: []string{"x"}}, func(string, string) {})
	assert.ErrorContains(t, err, "no image")
	_, err = b.Run(t.Context(), "r", runnerproto.Spec{Image: "missing/denied", Command: []string{"x"}}, func(string, string) {})
	assert.ErrorContains(t, err, "access denied")
	_, err = docker.New("ftp://x")
	assert.Error(t, err)
}

// TestDocker_Integration runs against a real engine (CI sets
// BALLET_DOCKER_TESTS=1; DOCKER_HOST or the default socket).
func TestDocker_Integration(t *testing.T) {
	if os.Getenv("BALLET_DOCKER_TESTS") != "1" {
		t.Skip("set BALLET_DOCKER_TESTS=1 to run against a real Docker engine")
	}
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	b, err := docker.New(host)
	require.NoError(t, err)
	require.NoError(t, b.Ping(t.Context()))

	var o output
	code, err := b.Run(t.Context(), "it-1", runnerproto.Spec{Image: "alpine:3.20",
		Command: []string{"sh", "-c", `echo "pwd=$(pwd) a=$A"; echo oops >&2; exit 3`}, Env: map[string]string{"A": "1"},
		Workdir: "repo"}, o.write)
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, "pwd=/workspace/repo a=1\n", o.text["stdout"])
	assert.Equal(t, "oops\n", o.text["stderr"])

	ctx, cancel := context.WithCancel(t.Context())
	go func() { time.Sleep(2 * time.Second); cancel() }()
	start := time.Now()
	_, err = b.Run(ctx, "it-2", runnerproto.Spec{Image: "alpine:3.20", Command: []string{"sleep", "300"}}, func(string, string) {})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 30*time.Second)
}
