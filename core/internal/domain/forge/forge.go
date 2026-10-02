// Package forge describes pull requests on git platforms (ADR-0007): code
// review happens there; Ballet links tickets to their pull requests,
// follows their state and acts on them through a forge adapter.
package forge

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// State of a pull request.
type State string

// States. Without a PR platform (generic git), a branch is "open" once
// pushed and "merged" once the default branch contains it.
const (
	StateOpen   State = "open"
	StateClosed State = "closed"
	StateMerged State = "merged"
)

// Checks summarises CI on the pull request's head.
type Checks string

// Check summaries.
const (
	ChecksNone    Checks = "none"
	ChecksPending Checks = "pending"
	ChecksSuccess Checks = "success"
	ChecksFailure Checks = "failure"
)

// Review summarises reviews.
type Review string

// Review summaries.
const (
	ReviewNone             Review = "none"
	ReviewApproved         Review = "approved"
	ReviewChangesRequested Review = "changes_requested"
	ReviewCommented        Review = "commented"
)

// PullRequest is a ticket branch on its way into the default branch.
type PullRequest struct {
	Number    int
	URL       string
	Title     string
	Head      string // ticket branch
	Base      string // default branch
	HeadSHA   string
	State     State
	Draft     bool
	Mergeable *bool // nil: not known yet
	Checks    Checks
	Review    Review
	UpdatedAt time.Time
}

// ReviewEvent is the verdict of a review Ballet posts.
type ReviewEvent string

// Review events.
const (
	ReviewApprove        ReviewEvent = "APPROVE"
	ReviewRequestChanges ReviewEvent = "REQUEST_CHANGES"
	ReviewComment        ReviewEvent = "COMMENT"
)

// Repo is a repository on a forge.
type Repo struct {
	URL   string // clone URL
	Owner string // GitHub-style owner and name, when the URL has them
	Name  string
	Token string // the project's git token; may be empty
}

// ErrUnsupported is returned for operations a forge cannot do.
var ErrUnsupported = errors.New("not supported by this forge")

// ErrNoBranch is returned when the ticket branch does not exist on the
// forge (nothing was pushed).
var ErrNoBranch = errors.New("the branch does not exist on the forge")

// Forge is a git platform adapter.
type Forge interface {
	Name() string
	// Ensure returns the pull request of head into base, opening it when
	// there is none.
	Ensure(ctx context.Context, r Repo, head, base, title, body string) (PullRequest, error)
	// Get refreshes a pull request.
	Get(ctx context.Context, r Repo, pr PullRequest) (PullRequest, error)
	Comment(ctx context.Context, r Repo, pr PullRequest, body string) error
	Review(ctx context.Context, r Repo, pr PullRequest, event ReviewEvent, body string) error
	// Merge squash-merges the pull request.
	Merge(ctx context.Context, r Repo, pr PullRequest, title string) error
}

var (
	scpRe  = regexp.MustCompile(`^[^@]+@([^:]+):([^/]+)/(.+?)(\.git)?/?$`)
	pathRe = regexp.MustCompile(`^/([^/]+)/(.+?)(\.git)?/?$`)
)

// ParseRepo extracts host, owner and name from a clone URL
// (https://host/owner/name(.git) or git@host:owner/name(.git)).
func ParseRepo(cloneURL string) (host, owner, name string, err error) {
	if m := scpRe.FindStringSubmatch(cloneURL); m != nil {
		return m[1], m[2], m[3], nil
	}
	u, err := url.Parse(cloneURL)
	if err != nil {
		return "", "", "", err
	}
	if m := pathRe.FindStringSubmatch(u.Path); m != nil && u.Host != "" && !strings.Contains(m[2], "/") {
		return u.Hostname(), m[1], m[2], nil
	}
	return "", "", "", fmt.Errorf("cannot find owner/name in %q", cloneURL)
}
