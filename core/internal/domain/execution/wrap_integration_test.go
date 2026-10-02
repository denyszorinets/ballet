package execution_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/execution"
)

// git runs git in dir with an isolated HOME.
func git(t *testing.T, dir, home string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// origin creates a bare repository with one commit on main.
func origin(t *testing.T) string {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	bare := filepath.Join(root, "origin.git")
	git(t, root, home, "init", "--quiet", "--bare", "--initial-branch=main", bare)
	seed := filepath.Join(root, "seed")
	git(t, root, home, "clone", "--quiet", bare, seed)
	require.NoError(t, os.WriteFile(filepath.Join(seed, "README"), []byte("hi\n"), 0o644))
	git(t, seed, home, "add", ".")
	git(t, seed, home, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "-m", "init")
	git(t, seed, home, "push", "--quiet", "origin", "main")
	return bare
}

// session runs the wrapped command in a fresh workspace, like a Runner.
func session(t *testing.T, s execution.Settings, branch string, env []string, command ...string) string {
	t.Helper()
	ws := t.TempDir()
	argv := execution.Wrap(s, branch, true, command)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = ws
	cmd.Env = append([]string{"HOME=" + filepath.Join(ws, ".home"), "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}, env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return string(out)
}

func TestWrap_PreparesARepositoryOnTheTicketBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	bare := origin(t)
	s := execution.Settings{RepoURL: "file://" + bare, DefaultBranch: "main", GitName: "Agent", GitEmail: "agent@x",
		Setup: []string{"echo setup-ran > .setup"}}

	out := session(t, s, "ballet/WEB-1-login", nil, "sh", "-c",
		`cat .setup; git rev-parse --abbrev-ref HEAD; echo work > f; git add f; git commit --quiet -m work; git push --quiet; git log -1 --format=%an`)
	assert.Contains(t, out, "setup-ran")
	assert.Contains(t, out, "ballet/WEB-1-login")
	assert.Contains(t, out, "Agent", "commits use the configured identity")
	home := t.TempDir()
	assert.Equal(t, "work", git(t, bare, home, "log", "-1", "--format=%s", "ballet/WEB-1-login"), "pushed")

	// The next stage continues on the same branch.
	out = session(t, s, "ballet/WEB-1-login", nil, "sh", "-c", `git log -1 --format=%s; cat f`)
	assert.Contains(t, out, "work\nwork")
}

func TestWrap_CredentialHelperHandsOutTheToken(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	s := execution.Settings{RepoURL: "file://" + origin(t)}
	out := session(t, s, "b", []string{execution.TokenEnv + "=s3cret"}, "sh", "-c",
		`printf 'protocol=https\nhost=example.com\n\n' | git credential fill; git remote get-url origin`)
	assert.Contains(t, out, "username=x-access-token")
	assert.Contains(t, out, "password=s3cret")
	assert.NotContains(t, strings.Split(out, "password=s3cret")[1], "s3cret", "the token is not in the remote URL")
}
