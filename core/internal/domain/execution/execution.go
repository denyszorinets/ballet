// Package execution describes how a project's runs execute: the
// repository and branch they work on, the devcontainer template, and the
// workspace preparation that runs before every agent session.
package execution

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// TokenEnv is the environment variable that carries the git token to a
// run. It is delivered with the run's start, never stored with the run.
const TokenEnv = "BALLET_GIT_TOKEN"

// RepoDir is where the repository is cloned, relative to the workspace.
const RepoDir = "repo"

// Settings are a project's execution settings.
type Settings struct {
	ProjectID      string
	RepoURL        string            // https://, ssh (git@host:path) or file://
	DefaultBranch  string            // "" : the repository's default
	Image          string            // devcontainer image (container backends)
	Setup          []string          // shell commands run in the repository before the session
	Env            map[string]string // added to every run's environment
	BranchTemplate string            // e.g. "ballet/{ticket}-{slug}"
	GitName        string            // commit identity
	GitEmail       string
	Forge          string // "github", "git" or "" (github for github.com, else git)
	ForgeAPIURL    string // GitHub Enterprise API; "" : https://api.github.com
	LinkTemplate   string // generic git: link of a branch, {branch} and {base}
	UpdatedAt      time.Time
	Version        int64
}

// DefaultBranchTemplate names ticket branches when a project sets none.
const DefaultBranchTemplate = "ballet/{ticket}-{slug}"

var (
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	scpRe    = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._/~-]+$`)
	badRefRe = regexp.MustCompile(`(^[/.]|[/.]$|\.\.|//|@\{|[\x00-\x20~^:?*\[\\\x7f]|\.lock$|\.lock/)`)
)

// Validate checks the settings.
func (s Settings) Validate() error {
	var errs []error
	if s.RepoURL != "" && !scpRe.MatchString(s.RepoURL) {
		u, err := url.Parse(s.RepoURL)
		switch {
		case err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" && u.Scheme != "file"):
			errs = append(errs, errors.New("repo_url must be an https, ssh, git@host:path or file URL"))
		case u.User != nil && u.Scheme != "ssh":
			errs = append(errs, errors.New("repo_url must not contain credentials; set a git token instead"))
		}
	}
	if s.DefaultBranch != "" && badRefRe.MatchString(s.DefaultBranch) {
		errs = append(errs, fmt.Errorf("default_branch %q is not a valid branch name", s.DefaultBranch))
	}
	tmpl := s.BranchTemplate
	if tmpl == "" {
		tmpl = DefaultBranchTemplate
	}
	if !strings.Contains(tmpl, "{ticket}") {
		errs = append(errs, errors.New("branch_template must contain {ticket}"))
	} else if _, err := BranchName(tmpl, "X-1", "feature", "t"); err != nil {
		errs = append(errs, err)
	}
	for k := range s.Env {
		if !envKeyRe.MatchString(k) {
			errs = append(errs, fmt.Errorf("invalid environment variable name %q", k))
		}
		if strings.HasPrefix(k, "BALLET_") {
			errs = append(errs, fmt.Errorf("environment variable %q is reserved", k))
		}
	}
	for _, c := range s.Setup {
		if strings.ContainsAny(c, "\n\r") || strings.TrimSpace(c) == "" {
			errs = append(errs, errors.New("each setup command must be one non-empty line"))
		}
	}
	switch s.Forge {
	case "", "github", "git":
	default:
		errs = append(errs, fmt.Errorf("forge %q must be github or git", s.Forge))
	}
	for name, v := range map[string]string{"forge_api_url": s.ForgeAPIURL, "link_template": s.LinkTemplate} {
		if v == "" {
			continue
		}
		if u, err := url.Parse(strings.NewReplacer("{branch}", "b", "{base}", "b").Replace(v)); err != nil ||
			(u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s must be an http(s) URL", name))
		}
	}
	if len(s.Image) > 300 || strings.ContainsAny(s.Image, " \t\n") {
		errs = append(errs, errors.New("image must be an image reference"))
	}
	return errors.Join(errs...)
}

// ForgeName is the forge of the repository: Forge, or github for
// github.com repositories, else git.
func (s Settings) ForgeName() string {
	if s.Forge != "" {
		return s.Forge
	}
	if strings.Contains(s.RepoURL, "github.com") {
		return "github"
	}
	return "git"
}

// Slug turns a title into a short branch-safe slug.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(title) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue // combining marks: é → e
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			dash = false
		default:
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		return "work"
	}
	return s
}

// BranchName fills a branch template: {ticket} (key), {slug} (of the
// title) and {type} (ticket type).
func BranchName(template, ticketKey, ticketType, title string) (string, error) {
	if template == "" {
		template = DefaultBranchTemplate
	}
	if ticketType == "" {
		ticketType = "feature"
	}
	b := strings.NewReplacer("{ticket}", ticketKey, "{slug}", Slug(title), "{type}", ticketType).Replace(template)
	if badRefRe.MatchString(b) || strings.ContainsAny(b, "{}") {
		return "", fmt.Errorf("branch name %q is not a valid git branch name", b)
	}
	return b, nil
}

// quote quotes s for a POSIX shell.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Wrap returns the command that prepares the workspace — git identity,
// credential helper (when withToken), clone, ticket branch, setup commands
// — and then executes command in the repository.
func Wrap(s Settings, branch string, withToken bool, command []string) []string {
	name, email := s.GitName, s.GitEmail
	if name == "" {
		name = "Ballet Agent"
	}
	if email == "" {
		email = "agent@ballet.invalid"
	}
	lines := []string{
		"set -eu",
		"echo 'ballet: preparing the workspace' >&2",
		`mkdir -p "$HOME"`,
		"git config --global user.name " + quote(name),
		"git config --global user.email " + quote(email),
		"git config --global init.defaultBranch main",
		"git config --global push.autoSetupRemote true",
	}
	if withToken {
		// The token stays in the environment: never in the remote URL or a
		// file.
		helper := `!f() { test "$1" = get || exit 0; echo username=x-access-token; echo "password=${` + TokenEnv + `}"; }; f`
		lines = append(lines, "git config --global credential.helper "+quote(helper))
	}
	lines = append(lines,
		"git clone --quiet "+quote(s.RepoURL)+" "+RepoDir,
		"cd "+RepoDir,
		"if git ls-remote --exit-code --heads origin "+quote(branch)+" >/dev/null 2>&1; then",
		"  git checkout --quiet -B "+quote(branch)+" "+quote("origin/"+branch),
	)
	if s.DefaultBranch != "" {
		// --no-track: the first push creates the ticket branch on origin
		// (push.autoSetupRemote) instead of targeting the default branch.
		lines = append(lines, "else",
			"  git checkout --quiet --no-track -B "+quote(branch)+" "+quote("origin/"+s.DefaultBranch))
	} else {
		lines = append(lines, "else", "  git checkout --quiet -B "+quote(branch))
	}
	lines = append(lines, "fi", "echo \"ballet: on branch $(git rev-parse --abbrev-ref HEAD)\" >&2")
	lines = append(lines, s.Setup...)
	lines = append(lines, `exec "$@"`)
	return append([]string{"sh", "-c", strings.Join(lines, "\n"), "ballet-workspace"}, command...)
}
