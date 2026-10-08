// Package failure tests recovery from crashes with Ballet's real binaries:
// Core, Knowledge, the LLM gateway and an agent (ADR-0025) run as
// processes, a fake agent stands in for Claude Code, and services are
// killed mid-stage. Run with `make failure-test` (BALLET_FAILURE_TESTS=1).
package failure_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth/oidctest"
)

var bin string // built binaries

func TestMain(m *testing.M) {
	if os.Getenv("BALLET_FAILURE_TESTS") != "1" {
		fmt.Println("skipping failure-injection tests (set BALLET_FAILURE_TESTS=1)")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "ballet-failure-bin")
	if err != nil {
		panic(err)
	}
	bin = dir
	root, _ := filepath.Abs("../../..")
	for _, pkg := range []string{"core/cmd/core", "gateway/cmd/gateway", "knowledge/cmd/knowledge", "agent/cmd/agent",
		"gateway/cmd/fake-anthropic"} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(bin, filepath.Base(pkg)), "./"+pkg)
		cmd.Dir, cmd.Stdout, cmd.Stderr = root, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// service is one Ballet process.
type service struct {
	t    *testing.T
	name string
	args []string
	env  []string
	dir  string
	log  *os.File
	cmd  *exec.Cmd
}

func (s *service) start() {
	s.t.Helper()
	s.cmd = exec.Command(filepath.Join(bin, s.name), s.args...)
	s.cmd.Dir, s.cmd.Env, s.cmd.Stdout, s.cmd.Stderr = s.dir, append(os.Environ(), s.env...), s.log, s.log
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // kill its sessions with it
	require.NoError(s.t, s.cmd.Start())
}

// kill crashes the service: SIGKILL, no shutdown.
func (s *service) kill() {
	s.t.Helper()
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	_ = s.cmd.Wait()
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

// stack is a running Ballet installation.
type stack struct {
	t                        *testing.T
	core, knowledge, gateway *service
	agent                    *service
	coreURL                  string
	token                    string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	dir := t.TempDir()
	iss := oidctest.NewIssuer(t)
	ports := map[string]string{}
	for _, s := range []string{"core", "knowledge", "gateway", "agent", "llm"} {
		ports[s] = freePort(t)
	}
	url := func(s string) string { return "http://localhost:" + ports[s] }
	agent, _ := filepath.Abs("fakeagent.sh")
	logs := filepath.Join(dir, "logs")
	require.NoError(t, os.MkdirAll(logs, 0o755))
	mk := func(name string, args []string, env ...string) *service {
		f, err := os.Create(filepath.Join(logs, name+".log"))
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = f.Close()
			if t.Failed() {
				data, _ := os.ReadFile(f.Name())
				t.Logf("--- %s log (tail) ---\n%s", name, tail(string(data), 3000))
			}
		})
		return &service{t: t, name: name, args: args, env: env, dir: dir, log: f}
	}
	st := &stack{t: t, coreURL: url("core")}
	llm := mk("fake-anthropic", []string{"-addr", "127.0.0.1:" + ports["llm"], "-key", "sk-fake"})
	st.core = mk("core", nil,
		"BALLET_CORE_SERVER_ADDR=:"+ports["core"],
		"BALLET_CORE_OIDC_ISSUER_URL="+iss.URL,
		"BALLET_CORE_RBAC_BOOTSTRAP_ORG_ADMINS=groups:ballet-admins",
		"BALLET_CORE_GATEWAY_URL="+url("gateway"),
		"BALLET_CORE_KNOWLEDGE_URL="+url("knowledge"),
		"BALLET_CORE_AGENTS_TRACKER_MCP_URL="+url("core")+"/mcp/tracker",
		"BALLET_CORE_SCHEDULER_INTERVAL=1s",
		"BALLET_CORE_RECONCILER_INTERVAL=2s",
	)
	st.knowledge = mk("knowledge", nil, "BALLET_KNOWLEDGE_SERVER_ADDR=:"+ports["knowledge"],
		"BALLET_KNOWLEDGE_CORE_URL="+url("core"))
	st.gateway = mk("gateway", nil, "BALLET_GATEWAY_SERVER_ADDR=:"+ports["gateway"],
		"BALLET_GATEWAY_CORE_URL="+url("core"), "BALLET_GATEWAY_ANTHROPIC_URL=http://127.0.0.1:"+ports["llm"])
	st.agent = mk("agent", nil, "BALLET_AGENT_SERVER_ADDR=:"+ports["agent"],
		"BALLET_AGENT_CORE_URL="+url("core"), "BALLET_AGENT_AGENT_NAME=a1", "BALLET_AGENT_DRIVERS_CLAUDE_COMMAND="+agent,
		"BALLET_AGENT_SESSION_WORK_DIR="+filepath.Join(dir, "work"))
	all := []*service{llm, st.core, st.knowledge, st.gateway, st.agent}
	t.Cleanup(func() {
		for _, s := range all {
			if s.cmd != nil && s.cmd.ProcessState == nil {
				s.kill()
			}
		}
	})
	llm.start()
	st.core.start()
	st.waitHealthy(url("core"))
	for _, tok := range []string{"knowledge", "gateway", "agent"} {
		st.eventually(10*time.Second, "token "+tok, func() bool {
			fi, err := os.Stat(filepath.Join(dir, "data", "service-tokens", tok+".token"))
			return err == nil && fi.Size() > 0
		})
	}
	st.knowledge.start()
	st.gateway.start()
	st.agent.start()
	st.waitHealthy(url("knowledge"))
	st.waitHealthy(url("gateway"))
	st.token = iss.Token(t, "alice", "ballet", map[string]any{"groups": []string{"ballet-admins"}, "name": "Alice"})
	st.seed(filepath.Join(dir, "origin.git"))
	return st
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func (st *stack) waitHealthy(url string) {
	st.t.Helper()
	st.eventually(30*time.Second, url+" healthy", func() bool {
		resp, err := http.Get(url + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
}

func (st *stack) eventually(timeout time.Duration, what string, ok func() bool) {
	st.t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			st.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// api calls Core's REST API as alice and decodes the response into out.
func (st *stack) api(method, path string, body, out any) int {
	st.t.Helper()
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(st.t, err)
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, st.coreURL+"/api/v1"+path, r)
	require.NoError(st.t, err)
	req.Header.Set("Authorization", "Bearer "+st.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0 // Core is down
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (st *stack) must(method, path string, body, out any) {
	st.t.Helper()
	code := st.api(method, path, body, out)
	require.True(st.t, code >= 200 && code < 300, "%s %s: %d", method, path, code)
}

// seed creates customer acme, project WEB with a repository and a
// one-stage pipeline, and an Anthropic credential for the fake LLM.
func (st *stack) seed(repo string) {
	t := st.t
	t.Helper()
	for _, c := range [][]string{{"git", "init", "-q", "--bare", "-b", "main", repo}} {
		require.NoError(t, exec.Command(c[0], c[1:]...).Run())
	}
	wc := repo + "-seed"
	require.NoError(t, exec.Command("git", "clone", "-q", repo, wc).Run())
	require.NoError(t, os.WriteFile(filepath.Join(wc, "README.md"), []byte("# Web\n"), 0o644))
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x"}, args...)...)
		cmd.Dir = wc
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("add", ".")
	git("commit", "-qm", "init")
	git("push", "-q", "origin", "HEAD:main")

	st.must("POST", "/customers", map[string]any{"key": "acme", "name": "Acme"}, nil)
	st.must("POST", "/customers/acme/projects", map[string]any{"key": "WEB", "name": "Web", "description": ""}, nil)
	st.must("PUT", "/customers/acme/credentials/anthropic", map[string]any{"api_key": "sk-fake"}, nil)
	var ex struct {
		Version int64 `json:"version"`
	}
	st.must("GET", "/projects/WEB/execution", nil, &ex)
	st.must("PUT", "/projects/WEB/execution", map[string]any{"repo_url": "file://" + repo, "default_branch": "main",
		"env": map[string]string{"FAKE_AGENT_SLEEP": "6"}, "version": ex.Version}, nil)
	st.must("PUT", "/projects/WEB/pipelines/default", map[string]any{"version": 0, "definition": map[string]any{
		"max_iterations": 1, "stages": []map[string]any{{"id": "work", "kind": "agent", "instructions": "Do the work."}}}}, nil)
}

// ticket creates a ready ticket (the scheduler starts it) and returns its
// key.
func (st *stack) ticket(title string) string {
	st.t.Helper()
	var it struct {
		Key     string `json:"key"`
		Version int64  `json:"version"`
	}
	st.must("POST", "/projects/WEB/items", map[string]any{"kind": "ticket", "title": title,
		"acceptance_criteria": []string{"it works"}}, &it)
	st.must("POST", "/items/"+it.Key+"/transition", map[string]any{"state": "ready", "version": it.Version}, nil)
	return it.Key
}

type flowView struct {
	Status  string `json:"status"`
	Waiting string `json:"waiting"`
	Run     string `json:"run"`
	Report  string `json:"report"`
}

func (st *stack) flow(key string) flowView {
	var f flowView
	st.api("GET", "/items/"+key+"/flow", nil, &f)
	return f
}

// running waits until the ticket's stage session runs and returns its run.
func (st *stack) running(key string) string {
	st.t.Helper()
	var run string
	var last string
	deadline := time.Now().Add(30 * time.Second)
	for {
		f := st.flow(key)
		var r map[string]any
		if f.Run != "" {
			st.api("GET", "/runs/"+f.Run, nil, &r)
		}
		last = fmt.Sprintf("flow=%+v run=%v", f, r)
		if r != nil && r["status"] == "running" {
			run = f.Run
			break
		}
		if time.Now().After(deadline) {
			st.t.Fatalf("timed out waiting for %s running: %s", key, last)
		}
		time.Sleep(250 * time.Millisecond)
	}
	time.Sleep(time.Second) // the agent made its first gateway call
	return run
}

func (st *stack) waitFlow(key, what string, timeout time.Duration, ok func(flowView) bool) flowView {
	st.t.Helper()
	var f flowView
	st.eventually(timeout, key+" "+what, func() bool { f = st.flow(key); return ok(f) })
	return f
}

func TestFailure_CoreCrashMidStageContinues(t *testing.T) {
	st := newStack(t)
	key := st.ticket("Survive a Core crash")
	st.running(key)

	st.core.kill()
	time.Sleep(2 * time.Second)
	st.core.start()
	st.waitHealthy(st.coreURL)

	// The agent keeps the session; its result reaches the restarted Core.
	st.waitFlow(key, "done", 60*time.Second, func(f flowView) bool { return f.Status == "done" })
}

func TestFailure_AgentCrashMidStageIsFlagged(t *testing.T) {
	st := newStack(t)
	key := st.ticket("Survive an agent crash")
	st.running(key)

	st.agent.kill()
	time.Sleep(time.Second)
	st.agent.start()

	// The restarted agent no longer has the session: the stage fails and
	// the ticket waits with a question for the humans.
	f := st.waitFlow(key, "flagged", 60*time.Second, func(f flowView) bool { return f.Waiting == "question" })
	require.Contains(t, f.Report, "no longer executes")
	var item struct {
		State string `json:"state"`
	}
	st.must("GET", "/items/"+key, nil, &item)
	require.Equal(t, "waiting_for_answer", item.State)
}

func TestFailure_GatewayRestartMidStageContinues(t *testing.T) {
	st := newStack(t)
	key := st.ticket("Survive a gateway restart")
	st.running(key)

	st.gateway.kill()
	time.Sleep(time.Second)
	st.gateway.start()

	// The session's next call reaches the restarted gateway.
	st.waitFlow(key, "done", 60*time.Second, func(f flowView) bool { return f.Status == "done" })
}

func TestFailure_GatewayDownAtTheEndIsFlagged(t *testing.T) {
	st := newStack(t)
	key := st.ticket("Lose the gateway")
	st.running(key)

	st.gateway.kill() // stays down: the session's last call fails
	f := st.waitFlow(key, "flagged", 60*time.Second, func(f flowView) bool { return f.Waiting == "question" })
	require.True(t, strings.Contains(f.Report, "gateway"), f.Report)
}
