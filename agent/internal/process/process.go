// Package process runs sessions as local processes in fresh workspaces,
// optionally as a separate, unprivileged OS user (ADR-0025). The container
// or VM the agent runs in is the isolation boundary.
package process

import (
	"bufio"
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
}

// Run executes spec.Command in a fresh workspace and streams its output.
func (b *Backend) Run(ctx context.Context, runID string, spec runnerproto.Spec, out func(stream, text string)) (int, error) {
	cred, err := b.credential()
	if err != nil {
		return -1, err
	}
	if b.WorkRoot != "" {
		// A configured work directory may not exist yet. The session user
		// must be able to reach its workspace inside it.
		if err := os.MkdirAll(b.WorkRoot, 0o711); err != nil {
			return -1, fmt.Errorf("create work directory: %w", err)
		}
		if cred != nil {
			if err := os.Chmod(b.WorkRoot, 0o711); err != nil {
				return -1, fmt.Errorf("work directory: %w", err)
			}
		}
	}
	ws, err := os.MkdirTemp(b.WorkRoot, "ballet-run-"+safe(runID)+"-")
	if err != nil {
		return -1, fmt.Errorf("create workspace: %w", err)
	}
	if !b.Keep {
		defer func() { _ = os.RemoveAll(ws) }()
	}
	home := filepath.Join(ws, ".home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return -1, fmt.Errorf("create workspace: %w", err)
	}
	for name, content := range spec.Files {
		p, err := inside(ws, name)
		if err != nil {
			return -1, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return -1, fmt.Errorf("write %s: %w", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return -1, fmt.Errorf("write %s: %w", name, err)
		}
	}
	dir, err := workdir(ws, spec.Workdir)
	if err != nil {
		return -1, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return -1, fmt.Errorf("create workdir: %w", err)
	}
	if cred != nil {
		if err := chownAll(ws, int(cred.Uid), int(cred.Gid)); err != nil {
			return -1, fmt.Errorf("hand the workspace to the session user: %w", err)
		}
	}

	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Dir = dir
	cmd.Env = environment(ws, home, spec.Env, spec.SecretEnv)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: cred}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	out(runnerproto.StreamSystem, fmt.Sprintf("process backend: workspace %s\n", ws))
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start %s: %w", spec.Command[0], err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go pump(&wg, stdout, runnerproto.StreamStdout, out)
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
