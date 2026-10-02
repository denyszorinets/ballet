package gitforge_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/infra/forge/gitforge"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%v: %s", args, out)
}

func TestGitForge_PushedThenMerged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	run(t, root, "init", "--quiet", "--bare", "--initial-branch=main", bare)
	wc := filepath.Join(root, "wc")
	run(t, root, "clone", "--quiet", bare, wc)
	require.NoError(t, os.WriteFile(filepath.Join(wc, "a"), []byte("a"), 0o644))
	run(t, wc, "add", ".")
	run(t, wc, "commit", "--quiet", "-m", "init")
	run(t, wc, "push", "--quiet", "origin", "main")

	g := gitforge.Adapter{LinkTemplate: "https://git.example.com/web/compare/{base}...{branch}"}
	repo := forge.Repo{URL: "file://" + bare}
	_, err := g.Ensure(t.Context(), repo, "ballet/WEB-1-x", "main", "t", "")
	assert.ErrorIs(t, err, forge.ErrNoBranch)

	run(t, wc, "checkout", "--quiet", "-b", "ballet/WEB-1-x")
	require.NoError(t, os.WriteFile(filepath.Join(wc, "b"), []byte("b"), 0o644))
	run(t, wc, "add", ".")
	run(t, wc, "commit", "--quiet", "-m", "work")
	run(t, wc, "push", "--quiet", "origin", "ballet/WEB-1-x")
	pr, err := g.Ensure(t.Context(), repo, "ballet/WEB-1-x", "main", "t", "")
	require.NoError(t, err)
	assert.Equal(t, forge.StateOpen, pr.State)
	assert.Equal(t, "https://git.example.com/web/compare/main...ballet/WEB-1-x", pr.URL)

	// Merged with a merge commit, then the branch is deleted.
	run(t, wc, "checkout", "--quiet", "main")
	run(t, wc, "merge", "--quiet", "--no-ff", "-m", "merge", "ballet/WEB-1-x")
	run(t, wc, "push", "--quiet", "origin", "main")
	run(t, wc, "push", "--quiet", "origin", "--delete", "ballet/WEB-1-x")
	pr, err = g.Get(t.Context(), repo, pr)
	require.NoError(t, err)
	assert.Equal(t, forge.StateMerged, pr.State)

	assert.ErrorIs(t, g.Merge(t.Context(), repo, pr, "x"), forge.ErrUnsupported)
}
