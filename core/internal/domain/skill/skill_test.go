package skill_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/skill"
)

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"gitflow", "code-review", "tdd2"} {
		assert.NoError(t, skill.ValidateName(ok), ok)
	}
	for _, bad := range []string{"g", "Gitflow", "git_flow", "-x", "x-", "1x", strings.Repeat("a", 65)} {
		assert.Error(t, skill.ValidateName(bad), bad)
	}
}

func TestContent_Validate(t *testing.T) {
	ok := skill.Content{Description: "Use for branching", Body: "# Rules", Files: map[string]string{"scripts/check.sh": "echo ok"}}
	require.NoError(t, ok.Validate())

	for name, c := range map[string]skill.Content{
		"no description": {Description: " "},
		"absolute path":  {Description: "d", Files: map[string]string{"/etc/passwd": ""}},
		"parent path":    {Description: "d", Files: map[string]string{"../x": ""}},
		"unclean path":   {Description: "d", Files: map[string]string{"a/../b": ""}},
		"skill md file":  {Description: "d", Files: map[string]string{"SKILL.md": ""}},
		"backslash":      {Description: "d", Files: map[string]string{`a\b`: ""}},
		"binary":         {Description: "d", Files: map[string]string{"x.bin": "\xff\xfe"}},
		"too large":      {Description: "d", Files: map[string]string{"big": strings.Repeat("x", 1<<20+1)}},
	} {
		assert.Error(t, c.Validate(), name)
	}
}

func TestParseScope(t *testing.T) {
	for in, want := range map[string]skill.Scope{
		"organization":  {Kind: skill.ScopeOrganization},
		"customer:acme": {Kind: skill.ScopeCustomer, Customer: "acme"},
		"project:WEB":   {Kind: skill.ScopeProject, Project: "WEB"},
	} {
		got, err := skill.ParseScope(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, in, got.String())
	}
	_, err := skill.ParseScope("team:x")
	assert.Error(t, err)
}
