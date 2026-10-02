// Package gitforge is the forge adapter for plain git hosting without a
// pull request platform (ADR-0007): a ticket branch is "open" once pushed
// and "merged" once the base branch contains it; links come from a URL
// template. Comments, reviews and merges are not supported.
package gitforge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
)

// Name of the adapter.
const Name = "git"

// Adapter inspects a repository with git.
type Adapter struct {
	// LinkTemplate builds a branch's link: {branch} and {base}; empty: no link.
	LinkTemplate string
}

// Name returns "git".
func (Adapter) Name() string { return Name }

// git runs git with the token available to its credential helper.
func git(ctx context.Context, dir, token string, args ...string) (string, error) {
	helper := `!f() { test "$1" = get || exit 0; echo username=x-access-token; echo "password=${` + execution.TokenEnv + `}"; }; f`
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "credential.helper=", "-c", "credential.helper=" + helper}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", execution.TokenEnv+"="+token)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// heads returns the SHA of each listed branch on the remote.
func heads(ctx context.Context, r forge.Repo, branches ...string) (map[string]string, error) {
	out, err := git(ctx, "", r.Token, append([]string{"ls-remote", "--heads", r.URL}, branches...)...)
	if err != nil {
		return nil, err
	}
	shas := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(line, "\t")
		if ok {
			shas[strings.TrimPrefix(ref, "refs/heads/")] = sha
		}
	}
	return shas, nil
}

// Ensure records the pushed branch; there is nothing to open.
func (a Adapter) Ensure(ctx context.Context, r forge.Repo, head, base, title, _ string) (forge.PullRequest, error) {
	pr := forge.PullRequest{Head: head, Base: base, Title: title}
	return a.Get(ctx, r, pr)
}

// Get finds whether the branch is pushed and whether base contains it.
func (a Adapter) Get(ctx context.Context, r forge.Repo, pr forge.PullRequest) (forge.PullRequest, error) {
	shas, err := heads(ctx, r, pr.Head, pr.Base)
	if err != nil {
		return forge.PullRequest{}, err
	}
	out := pr
	out.Checks, out.Review = forge.ChecksNone, forge.ReviewNone
	out.URL = strings.NewReplacer("{branch}", pr.Head, "{base}", pr.Base).Replace(a.LinkTemplate)
	headSHA, pushed := shas[pr.Head]
	if !pushed {
		if pr.HeadSHA == "" {
			return forge.PullRequest{}, fmt.Errorf("%w: %s is not pushed", forge.ErrNoBranch, pr.Head)
		}
		headSHA = pr.HeadSHA // deleted after merging, perhaps
	}
	out.HeadSHA, out.State = headSHA, forge.StateOpen
	baseSHA, ok := shas[pr.Base]
	if !ok {
		return out, nil
	}
	if baseSHA == headSHA {
		out.State = forge.StateMerged
		return out, nil
	}
	merged, err := contains(ctx, r, pr.Base, headSHA)
	if err != nil {
		return forge.PullRequest{}, err
	}
	if merged {
		out.State = forge.StateMerged
	}
	return out, nil
}

// contains reports whether branch base contains commit sha, fetching base
// into a temporary repository.
func contains(ctx context.Context, r forge.Repo, base, sha string) (bool, error) {
	dir, err := os.MkdirTemp("", "ballet-forge-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if _, err := git(ctx, dir, r.Token, "init", "--quiet", "--bare"); err != nil {
		return false, err
	}
	if _, err := git(ctx, dir, r.Token, "fetch", "--quiet", r.URL, "refs/heads/"+base+":refs/heads/base"); err != nil {
		return false, err
	}
	if _, err := git(ctx, dir, r.Token, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return false, nil // base does not have the commit at all
	}
	_, err = git(ctx, dir, r.Token, "merge-base", "--is-ancestor", sha, "base")
	return err == nil, nil
}

// Comment is not supported without a PR platform.
func (Adapter) Comment(context.Context, forge.Repo, forge.PullRequest, string) error {
	return forge.ErrUnsupported
}

// Review is not supported without a PR platform.
func (Adapter) Review(context.Context, forge.Repo, forge.PullRequest, forge.ReviewEvent, string) error {
	return forge.ErrUnsupported
}

// Merge is not supported: the integrate stage merges with git itself.
func (Adapter) Merge(context.Context, forge.Repo, forge.PullRequest, string) error {
	return forge.ErrUnsupported
}
