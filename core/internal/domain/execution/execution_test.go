package execution_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/execution"
)

func TestSlug(t *testing.T) {
	assert.Equal(t, "export-invoices-as-csv", execution.Slug("Export invoices as CSV!"))
	assert.Equal(t, "uber-cafe", execution.Slug("  Über   café  "))
	assert.Equal(t, "work", execution.Slug("!!!"))
	assert.LessOrEqual(t, len(execution.Slug("a very long title that goes on and on and on and on forever and ever")), 40)
}

func TestBranchName(t *testing.T) {
	b, err := execution.BranchName("ballet/{ticket}-{slug}", "WEB-12", "feature", "Login page")
	require.NoError(t, err)
	assert.Equal(t, "ballet/WEB-12-login-page", b)
	b, err = execution.BranchName("{type}/{ticket}_{slug}", "WEB-3", "bug", "Fix crash")
	require.NoError(t, err)
	assert.Equal(t, "bug/WEB-3_fix-crash", b)
	_, err = execution.BranchName("x..y/{ticket}", "WEB-1", "", "t")
	assert.Error(t, err, "not a valid git ref")
}

func TestValidate(t *testing.T) {
	s := execution.Settings{RepoURL: "https://github.com/acme/web.git", DefaultBranch: "main",
		BranchTemplate: "ballet/{ticket}-{slug}", Env: map[string]string{"CI": "1"}, Setup: []string{"make deps"}}
	require.NoError(t, s.Validate())
	for name, mutate := range map[string]func(*execution.Settings){
		"repo with credentials": func(s *execution.Settings) { s.RepoURL = "https://user:pw@github.com/a/b" },
		"repo scheme":           func(s *execution.Settings) { s.RepoURL = "ftp://x/y" },
		"no ticket in template": func(s *execution.Settings) { s.BranchTemplate = "ballet/{slug}" },
		"bad default branch":    func(s *execution.Settings) { s.DefaultBranch = "a b" },
		"bad env key":           func(s *execution.Settings) { s.Env = map[string]string{"1X": "y"} },
		"reserved env key":      func(s *execution.Settings) { s.Env = map[string]string{"BALLET_GIT_TOKEN": "y"} },
		"multi-line setup":      func(s *execution.Settings) { s.Setup = []string{"a\nb"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := s
			mutate(&c)
			assert.Error(t, c.Validate())
		})
	}
	assert.NoError(t, (execution.Settings{RepoURL: "git@github.com:acme/web.git", BranchTemplate: "{ticket}"}).Validate())
	assert.NoError(t, (execution.Settings{RepoURL: "file:///srv/repo.git", BranchTemplate: "{ticket}"}).Validate())
}

func TestWrap(t *testing.T) {
	s := execution.Settings{RepoURL: "https://example.com/it's.git", DefaultBranch: "main", GitName: "Ballet", GitEmail: "a@b",
		Setup: []string{"make deps"}}
	cmd := execution.Wrap(s, "ballet/WEB-1-x", true, []string{"claude", "-p", "hi"})
	require.Equal(t, "sh", cmd[0])
	assert.Equal(t, []string{"ballet-workspace", "claude", "-p", "hi"}, cmd[3:])
	script := cmd[2]
	assert.Contains(t, script, `git clone --quiet 'https://example.com/it'\''s.git' repo`)
	assert.Contains(t, script, "BALLET_GIT_TOKEN")
	assert.Contains(t, script, "make deps")
	assert.Contains(t, script, `exec "$@"`)
	assert.NotContains(t, execution.Wrap(s, "b", false, []string{"x"})[2], "credential.helper")
}
