// Package process runs sessions as local processes in fresh workspaces,
// optionally as a separate, unprivileged OS user (ADR-0025). The container
// or VM the agent runs in is the isolation boundary.
package process

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/denyszorinets/ballet/agent/internal/driver"
	"github.com/denyszorinets/ballet/kit/runnerproto"
)

// Backend executes runs as processes.
type Backend struct {
	WorkRoot string // parent of the run workspaces; default the OS temp dir
	Keep     bool   // keep workspaces after runs (debugging)
	// User, when set, is the OS user sessions run as; the agent must run
	// as root to switch to it. Sessions then cannot read the agent's files
	// (its token) or signal it.
	User string
	// StopGrace is how long a cancelled session gets after SIGTERM before
	// SIGKILL (default 10 s).
	StopGrace time.Duration
	// Drivers run coding-agent sessions (spec.Session), by runtime.
	Drivers map[string]driver.Driver

	mu   sync.Mutex
	live map[string]*session // running sessions by run
}

// Input delivers a human's input to the running session of a run.
func (b *Backend) Input(runID, kind, text string) error {
	b.mu.Lock()
	s := b.live[runID]
	b.mu.Unlock()
	if s == nil {
		return ErrNoSession
	}
	return s.input(kind, text)
}

// Run executes a run in a fresh workspace and streams its output: the
// spec's command, then its coding-agent session, if any. The result is
// the session's (nil without one, or when it reported none).
func (b *Backend) Run(ctx context.Context, runID string, spec runnerproto.Spec, out func(stream, text string)) (int, *runnerproto.Result, error) {
	var drv driver.Driver
	var setup driver.Setup
	if spec.Session != nil {
		var ok bool
		if drv, ok = b.Drivers[spec.Session.Runtime]; !ok {
			return -1, nil, fmt.Errorf("unknown agent runtime %q", spec.Session.Runtime)
		}
		var err error
		sess := *spec.Session
		if sess.Resume != nil {
			state, err := gunzip(sess.Resume.State)
			if err != nil {
				return -1, nil, fmt.Errorf("session state: %w", err)
			}
			sess.Resume = &runnerproto.Resume{SessionID: sess.Resume.SessionID, State: state}
		}
		if setup, err = drv.Setup(sess); err != nil {
			return -1, nil, err
		}
	}
	cred, err := b.credential()
	if err != nil {
		return -1, nil, err
	}
	ws, err := b.workspace(runID, cred)
	if err != nil {
		return -1, nil, err
	}
	if !b.Keep {
		defer func() { _ = os.RemoveAll(ws) }()
	}
	home := filepath.Join(ws, ".home")
	files := map[string]string{}
	for name, content := range spec.Files {
		files[name] = content
	}
	for name, content := range setup.Files {
		files[filepath.Join(".home", name)] = content
	}
	if err := write(ws, files); err != nil {
		return -1, nil, err
	}
	dir, err := workdir(ws, spec.Workdir)
	if err != nil {
		return -1, nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return -1, nil, fmt.Errorf("create workdir: %w", err)
	}
	if cred != nil {
		if err := chownAll(ws, int(cred.Uid), int(cred.Gid)); err != nil {
			return -1, nil, fmt.Errorf("hand the workspace to the session user: %w", err)
		}
	}
	out(runnerproto.StreamSystem, fmt.Sprintf("agent: workspace %s\n", ws))

	env := environment(ws, home, spec.Env, spec.SecretEnv)
	if len(spec.Command) > 0 {
		code, err := b.exec(ctx, proc{argv: spec.Command, dir: dir, env: env, cred: cred}, out, nil)
		if err != nil || code != 0 || spec.Session == nil {
			return code, nil, err
		}
	}
	sdir, err := workdir(ws, spec.Session.Dir)
	if err != nil {
		return -1, nil, err
	}
	vars := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		vars[k] = v
	}
	for k, v := range setup.Env {
		env = append(env, k+"="+os.Expand(v, func(name string) string { return vars[name] }))
	}
	s := &session{drv: drv.NewCodec(sdir), prompt: spec.Session.Prompt, out: out}
	b.mu.Lock()
	if b.live == nil {
		b.live = map[string]*session{}
	}
	b.live[runID] = s
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.live, runID)
		b.mu.Unlock()
	}()
	code, err := b.exec(ctx, proc{argv: setup.Command, dir: sdir, env: env, cred: cred}, out, s)
	res, parked, sessionID := s.result()
	if err != nil || !parked {
		return code, res, err
	}
	// Parked: keep the work and what continues the session.
	if _, err := os.Stat(filepath.Join(sdir, ".git")); err == nil {
		_, _ = b.exec(ctx, proc{argv: []string{"sh", "-c", pushWIP}, dir: sdir, env: env, cred: cred}, out, nil)
	}
	parkedRes := runnerproto.Result{Success: true, Parked: true, SessionID: sessionID}
	if res != nil {
		parkedRes.Summary, parkedRes.Turns, parkedRes.CostUSD = res.Summary, res.Turns, res.CostUSD
	}
	if state, err := drv.State(home, sessionID); err != nil {
		out(runnerproto.StreamSystem, fmt.Sprintf("agent: session state not saved: %v\n", err))
	} else if gz, err := gzipState(state); err != nil {
		out(runnerproto.StreamSystem, fmt.Sprintf("agent: session state not saved: %v\n", err))
	} else {
		parkedRes.State = gz
	}
	out(runnerproto.StreamSystem, "agent: session parked\n")
	return code, &parkedRes, nil
}

// pushWIP commits and pushes the work in progress of a parked session.
const pushWIP = `echo "agent: pushing the work in progress" >&2
git add -A && { git diff --cached --quiet || git commit -q -m "WIP: parked while waiting for answers"; } && git push -q`

// maxState bounds a saved session state, compressed: it travels to Core
// in one message.
const maxState = 3 << 20

func gzipState(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > maxState {
		return nil, fmt.Errorf("%d bytes compressed, more than %d", buf.Len(), maxState)
	}
	return buf.Bytes(), nil
}

func gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 256<<20))
}

// workspace creates a fresh workspace with its HOME.
func (b *Backend) workspace(runID string, cred *syscall.Credential) (string, error) {
	if b.WorkRoot != "" {
		// A configured work directory may not exist yet. The session user
		// must be able to reach its workspace inside it.
		if err := os.MkdirAll(b.WorkRoot, 0o711); err != nil {
			return "", fmt.Errorf("create work directory: %w", err)
		}
		if cred != nil {
			if err := os.Chmod(b.WorkRoot, 0o711); err != nil {
				return "", fmt.Errorf("work directory: %w", err)
			}
		}
	}
	ws, err := os.MkdirTemp(b.WorkRoot, "ballet-run-"+safe(runID)+"-")
	if err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".home"), 0o700); err != nil {
		_ = os.RemoveAll(ws)
		return "", fmt.Errorf("create workspace: %w", err)
	}
	return ws, nil
}

// write writes workspace-relative files.
func write(ws string, files map[string]string) error {
	for name, content := range files {
		p, err := inside(ws, name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// proc is a process to execute.
type proc struct {
	argv []string
	dir  string
	env  []string
	cred *syscall.Credential
}

// exec runs a process to its end. Without a session its output is
// streamed as is; with one, the session's driver talks to it over
// standard input and output.
func (b *Backend) exec(ctx context.Context, p proc, out func(stream, text string), s *session) (int, error) {
	cmd := exec.Command(p.argv[0], p.argv[1:]...)
	cmd.Dir = p.dir
	cmd.Env = p.env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: p.cred}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	var stdin io.WriteCloser
	if s != nil {
		if stdin, err = cmd.StdinPipe(); err != nil {
			return -1, err
		}
	}
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start %s: %w", p.argv[0], err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	if s == nil {
		go pump(&wg, stdout, runnerproto.StreamStdout, out)
	} else {
		s.begin(stdin)
		go s.read(&wg, stdout)
	}
	go pump(&wg, stderr, runnerproto.StreamStderr, out)

	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			b.stop(cmd)
		case <-stopped:
		}
	}()
	wg.Wait()
	if s != nil {
		s.end()
	}
	err = cmd.Wait()
	close(stopped)
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// credential resolves User; nil when sessions run as the agent's user.
func (b *Backend) credential() (*syscall.Credential, error) {
	if b.User == "" {
		return nil, nil
	}
	u, err := user.Lookup(b.User)
	if err != nil {
		return nil, fmt.Errorf("session user: %w", err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("session user %s: uid %q", b.User, u.Uid)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("session user %s: gid %q", b.User, u.Gid)
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}, nil
}

// chownAll gives the tree at root to uid:gid.
func chownAll(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// stop terminates the session's process group, forcefully after StopGrace.
func (b *Backend) stop(cmd *exec.Cmd) {
	pgid := -cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	grace := b.StopGrace
	if grace <= 0 {
		grace = 10 * time.Second
	}
	time.AfterFunc(grace, func() { _ = syscall.Kill(pgid, syscall.SIGKILL) })
}

// pump forwards output in line-sized pieces.
func pump(wg *sync.WaitGroup, r io.Reader, stream string, out func(stream, text string)) {
	defer wg.Done()
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			out(stream, line)
		}
		if err != nil {
			return
		}
	}
}

// workdir resolves the spec's workdir, relative to the workspace.
func workdir(ws, rel string) (string, error) {
	rel = strings.TrimPrefix(filepath.Clean("/"+rel), "/")
	return filepath.Join(ws, rel), nil
}

// inside resolves a workspace-relative file path, refusing escapes.
func inside(ws, name string) (string, error) {
	clean := filepath.Clean(name)
	if name == "" || filepath.IsAbs(name) || clean == "." || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("file path %q must be relative to the workspace", name)
	}
	return filepath.Join(ws, clean), nil
}

// environment is minimal: the session does not inherit the agent's
// configuration or secrets.
func environment(ws, home string, envs ...map[string]string) []string {
	out := []string{"HOME=" + home, "BALLET_WORKSPACE=" + ws}
	for _, k := range []string{"PATH", "LANG", "TZ"} {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	for _, env := range envs {
		for k, v := range env {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, s)
}
