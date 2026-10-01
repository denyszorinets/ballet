package skill_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/core/internal/domain/skill"
)

func sk(scope skill.ScopeKind, name string, latest int64) skill.Skill {
	return skill.Skill{ID: string(scope) + "/" + name, Scope: skill.Scope{Kind: scope}, Name: name, LatestVersion: latest}
}

func TestResolve(t *testing.T) {
	skills := []skill.Skill{
		sk(skill.ScopeOrganization, "code-review", 3),
		sk(skill.ScopeProject, "code-review", 1), // project override
		sk(skill.ScopeOrganization, "gitflow", 5),
		sk(skill.ScopeCustomer, "gitflow", 2), // customer override
		sk(skill.ScopeOrganization, "docs", 4),
		sk(skill.ScopeOrganization, "draft-only", 0), // never published
		sk(skill.ScopeOrganization, "legacy", 2),
	}
	pins := []skill.Pin{
		{Name: "docs", Version: 2},
		{Name: "legacy", Disabled: true},
		{Name: "gitflow", Version: 9},
		{Name: "ghost", Version: 1},
	}

	got := skill.Resolve(skills, pins)

	byName := map[string]skill.Resolved{}
	for _, r := range got {
		byName[r.Name] = r
	}
	assert.Equal(t, []string{"code-review", "docs", "ghost", "gitflow"}, func() (n []string) {
		for _, r := range got {
			n = append(n, r.Name)
		}
		return
	}(), "sorted; draft-only and disabled excluded")
	assert.Equal(t, skill.ScopeProject, byName["code-review"].Skill.Scope.Kind, "most specific scope wins")
	assert.Equal(t, int64(1), byName["code-review"].Version, "latest of the winning skill")
	assert.Equal(t, int64(2), byName["docs"].Version)
	assert.True(t, byName["docs"].Pinned)
	assert.Equal(t, skill.ScopeCustomer, byName["gitflow"].Skill.Scope.Kind)
	assert.NotEmpty(t, byName["gitflow"].Problem, "pin beyond the latest version")
	assert.Zero(t, byName["gitflow"].Version)
	assert.NotEmpty(t, byName["ghost"].Problem, "pin to an unknown skill")
}
