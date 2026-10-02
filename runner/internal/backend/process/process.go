// Package process runs sessions as local processes in temporary
// workspaces — for development only, without isolation (ADR-0023).
package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	// StopGrace is how long a cancelled session gets after SIGTERM before
	// SIGKILL (default 10 s).
	StopGrace time.Duration
}

// Run executes spec.Command in a fresh workspace and streams its output.
func (b *Backend) Run(ctx context.Context, runID string, spec runnerproto.Spec, out func(stream, text string)) (int, error) {
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
	dir, err := workdir(ws, spec.Workdir)
	if err != nil {
		return -1, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return -1, fmt.Errorf("create workdir: %w", err)
	}

	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Dir = dir
	cmd.Env = environment(ws, home, spec.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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

// environment is minimal: the session does not inherit the Runner's
// configuration or secrets.
func environment(ws, home string, env map[string]string) []string {
	out := []string{"HOME=" + home, "BALLET_WORKSPACE=" + ws}
	for _, k := range []string{"PATH", "LANG", "TZ"} {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	for k, v := range env {
		out = append(out, k+"="+v)
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
