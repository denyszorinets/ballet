package process_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/agent/internal/driver"
	"github.com/denyszorinets/ballet/agent/internal/process"
	"github.com/denyszorinets/ballet/kit/agentproto"
)

type output struct {
	mu     sync.Mutex
	stdout strings.Builder
	stderr strings.Builder
	system strings.Builder
	event  strings.Builder
}

func (o *output) write(stream, text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch stream {
	case agentproto.StreamStdout:
		o.stdout.WriteString(text)
	case agentproto.StreamStderr:
		o.stderr.WriteString(text)
	case agentproto.StreamEvent:
		o.event.WriteString(text)
	default:
		o.system.WriteString(text)
	}
}

func sh(script string, env map[string]string) agentproto.Spec {
	return agentproto.Spec{Command: []string{"sh", "-c", script}, Env: env, Workdir: "repo"}
}

func TestProcess_RunsInAFreshWorkspace(t *testing.T) {
	root := t.TempDir()
	b := &process.Backend{WorkRoot: root}
	var o output
	t.Setenv("BALLET_AGENT_SECRET", "do-not-leak")
	spec := sh(`pwd; echo "home=$HOME"; echo "x=$X tok=$TOK"; echo "secret=$BALLET_AGENT_SECRET"; echo oops >&2; touch f; exit 3`,
		map[string]string{"X": "1"})
	spec.SecretEnv = map[string]string{"TOK": "t"}
	code, _, err := b.Run(t.Context(), "run-1", spec, o.write)
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	lines := strings.Split(strings.TrimSpace(o.stdout.String()), "\n")
	require.Len(t, lines, 4)
	assert.True(t, strings.HasSuffix(lines[0], "/repo"), lines[0])
	assert.Contains(t, lines[1], "/.home")
	assert.Equal(t, "x=1 tok=t", lines[2], "secret env is passed to the session")
	assert.Equal(t, "secret=", lines[3], "the agent's environment is not inherited")
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
		_, _, err := b.Run(ctx, "run-2", sh(`(sleep 30; echo child) & echo started; wait`, nil), o.write)
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
	_, _, err := b.Run(t.Context(), "run-3", agentproto.Spec{Command: []string{"/no/such/binary"}}, func(string, string) {})
	assert.ErrorContains(t, err, "start /no/such/binary")

	var o output
	code, _, err := b.Run(t.Context(), "run-4", sh(`pwd`, nil), o.write)
	require.NoError(t, err)
	assert.Zero(t, code)
	dir := strings.TrimSpace(o.stdout.String())
	_, statErr := os.Stat(dir)
	assert.NoError(t, statErr, "Keep keeps workspaces")
	assert.True(t, strings.HasPrefix(dir, b.WorkRoot), "workdirs stay inside the workspace: %s", dir)

	o = output{}
	_, _, err = b.Run(t.Context(), "run-5", agentproto.Spec{Command: []string{"pwd"}, Workdir: "../../etc"}, o.write)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(o.stdout.String()), b.WorkRoot), "no escaping the workspace")
	_ = filepath.Join
}

func TestProcess_WritesFilesBeforeTheSession(t *testing.T) {
	b := &process.Backend{WorkRoot: t.TempDir()}
	var o output
	spec := agentproto.Spec{Command: []string{"sh", "-c", `cat "$HOME/.claude/skills/x/SKILL.md" ../.ballet/mcp.json`}, Workdir: "repo",
		Files: map[string]string{".home/.claude/skills/x/SKILL.md": "skill\n", ".ballet/mcp.json": "{}\n"}}
	code, _, err := b.Run(t.Context(), "run-f", spec, o.write)
	require.NoError(t, err)
	assert.Zero(t, code, o.stderr.String())
	assert.Equal(t, "skill\n{}\n", o.stdout.String())

	_, _, err = b.Run(t.Context(), "run-g", agentproto.Spec{Command: []string{"true"}, Files: map[string]string{"../x": "no"}}, o.write)
	assert.ErrorContains(t, err, "relative to the workspace")
}

func TestProcess_CreatesAMissingWorkDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not", "yet")
	b := &process.Backend{WorkRoot: root}
	var o output
	code, _, err := b.Run(t.Context(), "run-1", sh("true", nil), o.write)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	_, err = os.Stat(root)
	assert.NoError(t, err)
}

func TestProcess_RunsTheSessionAsTheSessionUser(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("switching users needs root")
	}
	root := t.TempDir()
	require.NoError(t, os.Chmod(filepath.Dir(root), 0o711)) // the session user reaches its workspace
	secret := filepath.Join(t.TempDir(), "agent.token")
	require.NoError(t, os.WriteFile(secret, []byte("token"), 0o600))
	b := &process.Backend{WorkRoot: root, User: "nobody"}
	var o output
	spec := sh(`id -u; touch made-here && echo wrote; cat `+secret+` || echo denied`, nil)
	spec.Files = map[string]string{"repo/.keep": ""}
	code, _, err := b.Run(t.Context(), "run-u", spec, o.write)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "65534\nwrote\ndenied\n", o.stdout.String(), o.stderr.String())
}

func TestProcess_UnknownSessionUserFails(t *testing.T) {
	b := &process.Backend{WorkRoot: t.TempDir(), User: "no-such-user-ballet"}
	var o output
	_, _, err := b.Run(t.Context(), "run-x", sh("true", nil), o.write)
	assert.ErrorContains(t, err, "no-such-user-ballet")
}

// fakeDriver drives a shell script: "EV <text>" lines are text events,
// "RESULT <text>" ends a turn.
type fakeDriver struct{ script string }

func (d fakeDriver) Setup(s agentproto.Session) (driver.Setup, error) {
	files := map[string]string{"cfg/skill.md": "skill"}
	env := map[string]string{"KEY": "${" + s.TokenEnv + "}"}
	if s.Resume != nil {
		files["state-"+s.Resume.SessionID] = string(s.Resume.State)
		env["RESUMED"] = s.Resume.SessionID
	}
	return driver.Setup{Files: files, Env: env, Command: []string{"sh", "-c", d.script}}, nil
}

func (fakeDriver) State(home, sessionID string) ([]byte, error) {
	return os.ReadFile(filepath.Join(home, "state-"+sessionID))
}

func (d fakeDriver) NewCodec(string) driver.Codec { return d }

func (d fakeDriver) Start(prompt string) []byte { return d.Message(prompt) }

func (fakeDriver) Message(text string) []byte { return []byte(text + "\n") }

func (fakeDriver) Interrupt() []byte { return []byte("INTERRUPT\n") }

func (fakeDriver) Parse(line []byte) driver.Parsed {
	l := string(line)
	switch {
	case strings.HasPrefix(l, "EV "):
		return driver.Parsed{Events: []agentproto.Event{{Kind: "text", Text: l[3:]}}}
	case strings.HasPrefix(l, "RESULT "):
		return driver.Parsed{Result: &agentproto.Result{Success: true, Summary: l[7:], Turns: 1}}
	case strings.HasPrefix(l, "SID "):
		return driver.Parsed{SessionID: l[4:]}
	}
	return driver.Parsed{}
}

func (o *output) events(t *testing.T) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var texts []string
	for _, l := range strings.Split(strings.TrimSpace(o.event.String()), "\n") {
		var e agentproto.Event
		require.NoError(t, json.Unmarshal([]byte(l), &e))
		texts = append(texts, e.Text)
	}
	return texts
}

func TestProcess_RunsASessionThroughItsDriver(t *testing.T) {
	b := &process.Backend{WorkRoot: t.TempDir(), Drivers: map[string]driver.Driver{"fake": fakeDriver{script: `
read prompt
echo "EV prompt=$prompt"
echo "EV dir=$(basename "$PWD") key=$KEY skill=$(cat "$HOME/cfg/skill.md") prepared=$(cat ../prepared)"
echo "RESULT finished"
cat >/dev/null
echo "EV stdin closed"`}}}
	var o output
	spec := agentproto.Spec{
		Command:   []string{"sh", "-c", "mkdir repo && echo yes > prepared && echo preparing"},
		SecretEnv: map[string]string{"TOK": "secret"},
		Session:   &agentproto.Session{Runtime: "fake", Prompt: "do it", TokenEnv: "TOK", Dir: "repo"},
	}
	code, res, err := b.Run(t.Context(), "run-s", spec, o.write)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, &agentproto.Result{Success: true, Summary: "finished", Turns: 1}, res)
	assert.Equal(t, "preparing\n", o.stdout.String(), "the preparation's output is streamed as is")
	assert.Equal(t, []string{"prompt=do it", "dir=repo key=secret skill=skill prepared=yes", "stdin closed"}, o.events(t),
		"the prompt is sent, and standard input closes when the turn ends")
}

func TestProcess_FailedPreparationSkipsTheSession(t *testing.T) {
	b := &process.Backend{WorkRoot: t.TempDir(), Drivers: map[string]driver.Driver{"fake": fakeDriver{script: `echo "EV ran"`}}}
	var o output
	code, res, err := b.Run(t.Context(), "run-p", agentproto.Spec{Command: []string{"sh", "-c", "exit 4"},
		Session: &agentproto.Session{Runtime: "fake", Prompt: "x"}}, o.write)
	require.NoError(t, err)
	assert.Equal(t, 4, code)
	assert.Nil(t, res)
	assert.Empty(t, o.event.String())
}

func TestProcess_UnknownRuntimeFails(t *testing.T) {
	b := &process.Backend{WorkRoot: t.TempDir()}
	var o output
	_, _, err := b.Run(t.Context(), "run-r", agentproto.Spec{Session: &agentproto.Session{Runtime: "nope", Prompt: "x"}}, o.write)
	assert.ErrorContains(t, err, `unknown agent runtime "nope"`)
}

// running starts a session of the fake driver in the background; done
// receives its result.
func running(t *testing.T, script string) (*process.Backend, *output, chan *agentproto.Result) {
	t.Helper()
	return runningSpec(t, script, agentproto.Spec{Session: &agentproto.Session{Runtime: "fake", Prompt: "go"}})
}

func runningSpec(t *testing.T, script string, spec agentproto.Spec) (*process.Backend, *output, chan *agentproto.Result) {
	t.Helper()
	b := &process.Backend{WorkRoot: t.TempDir(), Drivers: map[string]driver.Driver{"fake": fakeDriver{script: script}}}
	o := &output{}
	done := make(chan *agentproto.Result, 1)
	go func() {
		_, res, err := b.Run(t.Context(), "run-i", spec, o.write)
		assert.NoError(t, err)
		done <- res
	}()
	return b, o, done
}

func (o *output) waitFor(t *testing.T, text string) {
	t.Helper()
	require.Eventually(t, func() bool { o.mu.Lock(); defer o.mu.Unlock(); return strings.Contains(o.event.String(), text) },
		5*time.Second, 10*time.Millisecond)
}

func TestProcess_MessagesWaitForTheEndOfTheTurn(t *testing.T) {
	b, o, done := running(t, `
read p
echo "EV started"
read gate
echo "RESULT one"
read m
echo "EV got $m"
echo "RESULT two"
cat >/dev/null`)
	o.waitFor(t, "started")
	require.NoError(t, b.Input("run-i", agentproto.InputMessage, "more please"))
	assert.ErrorIs(t, b.Input("run-i", agentproto.InputMessage, ""), process.ErrInvalidInput)
	assert.ErrorIs(t, b.Input("run-i", "shout", "x"), process.ErrInvalidInput)
	// The interrupt line releases the script's gate; nothing is queued with it.
	require.NoError(t, b.Input("run-i", agentproto.InputInterrupt, ""))
	res := <-done
	assert.Equal(t, &agentproto.Result{Success: true, Summary: "two", Turns: 2}, res)
	assert.Equal(t, []string{"started", "more please", "got more please"}, o.events(t),
		"the message is shown when it reaches the session")
	assert.ErrorIs(t, b.Input("run-i", agentproto.InputMessage, "late"), process.ErrNoSession)
}

func TestProcess_InterruptStopsTheTurnThenDelivers(t *testing.T) {
	b, o, done := running(t, `
read p
echo "EV started"
read x
echo "EV saw $x"
echo "RESULT stopped"
read m
echo "EV got $m"
echo "RESULT two"
cat >/dev/null`)
	o.waitFor(t, "started")
	require.NoError(t, b.Input("run-i", agentproto.InputInterrupt, "do this instead"))
	<-done
	assert.Equal(t, []string{"started", "saw INTERRUPT", "do this instead", "got do this instead"}, o.events(t))
}

// hold holds a fake session that printed "ready" and waits for a line,
// then releases it with an interrupt line so its turn ends.
func hold(t *testing.T, b *process.Backend, o *output) {
	t.Helper()
	o.waitFor(t, "ready")
	require.NoError(t, b.Input("run-i", agentproto.InputHold, ""))
	require.NoError(t, b.Input("run-i", agentproto.InputInterrupt, ""))
	o.waitFor(t, "asked")
}

func TestProcess_AHeldSessionWaitsAndTakesMessagesAtOnce(t *testing.T) {
	b, o, done := running(t, `
read p
echo "EV ready"
read gate
echo "EV asked"
echo "RESULT waiting"
read answer
echo "EV answer=$answer"
echo "RESULT continued"
cat >/dev/null`)
	hold(t, b, o)
	time.Sleep(100 * time.Millisecond) // the turn has ended: the session waits
	require.NoError(t, b.Input("run-i", agentproto.InputMessage, "Postgres"))
	o.waitFor(t, "answer=Postgres")
	time.Sleep(100 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("a held session stays open after its turn")
	default:
	}
	require.NoError(t, b.Input("run-i", agentproto.InputRelease, ""))
	res := <-done
	assert.Equal(t, "continued", res.Summary)
	assert.Equal(t, 2, res.Turns)
	assert.False(t, res.Parked)
}

func TestProcess_ParkingPushesTheWorkAndSavesTheState(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	spec := agentproto.Spec{
		Command: []string{"sh", "-c", "git init -q --bare " + origin + " && git clone -q " + origin + " repo"},
		Env: map[string]string{"GIT_AUTHOR_NAME": "a", "GIT_AUTHOR_EMAIL": "a@x", "GIT_COMMITTER_NAME": "a",
			"GIT_COMMITTER_EMAIL": "a@x"},
		Session: &agentproto.Session{Runtime: "fake", Prompt: "go", Dir: "repo"},
	}
	b, o, done := runningSpec(t, `
read p
echo "SID s-1"
echo "transcript of s-1" > "$HOME/state-s-1"
echo "half done" > work.txt
echo "EV ready"
read gate
echo "EV asked"
echo "RESULT waiting"
cat >/dev/null`, spec)
	hold(t, b, o)
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, b.Input("run-i", agentproto.InputPark, ""))
	res := <-done
	require.NotNil(t, res)
	assert.True(t, res.Parked)
	assert.Equal(t, "s-1", res.SessionID)
	assert.Equal(t, "waiting", res.Summary)
	zr, err := gzip.NewReader(bytes.NewReader(res.State))
	require.NoError(t, err)
	state, _ := io.ReadAll(zr)
	assert.Equal(t, "transcript of s-1\n", string(state))
	log, err := exec.Command("git", "--git-dir", origin, "log", "--all", "--format=%s", "--name-only").CombinedOutput()
	require.NoError(t, err, string(log))
	assert.Contains(t, string(log), "WIP: parked while waiting for answers")
	assert.Contains(t, string(log), "work.txt")
}

func TestProcess_ResumesAParkedSession(t *testing.T) {
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte("earlier turns"))
	require.NoError(t, w.Close())
	_, o, done := runningSpec(t, `
read p
echo "EV resumed=$RESUMED state=$(cat "$HOME/state-s-1") message=$p"
echo "RESULT done"
cat >/dev/null`, agentproto.Spec{Session: &agentproto.Session{Runtime: "fake", Prompt: "The answer is 42.",
		Resume: &agentproto.Resume{SessionID: "s-1", State: gz.Bytes()}}})
	<-done
	assert.Equal(t, []string{"resumed=s-1 state=earlier turns message=The answer is 42."}, o.events(t))
}
