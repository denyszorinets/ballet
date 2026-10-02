package onboarding_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
)

func bundle() onboarding.Bundle {
	return onboarding.Bundle{
		Project: "WEB", Stage: "implement", StageInstructions: "Implement the ticket with tests.",
		Ticket: onboarding.Item{Key: "WEB-3", Kind: "ticket", Title: "Login", State: "in_progress",
			Description: "Users sign in with OIDC."},
		Type: "feature", AcceptanceCriteria: []string{"OIDC sign-in works", "Logout clears the session"},
		ReviewMode: "agent", MergeMode: "auto", Branch: "ballet/WEB-3-login",
		Epic:      &onboarding.Item{Key: "WEB-1", Kind: "epic", Title: "Auth", Description: "Everything about accounts."},
		Milestone: &onboarding.Item{Key: "WEB-0", Kind: "milestone", Title: "MVP"},
		Dependencies: []onboarding.Dependency{{Item: onboarding.Item{Key: "WEB-2", Title: "Session store", State: "done"},
			Relation: "blocked_by", Report: "Added a Redis session store.\nSee pkg/session."}},
		Knowledge: []onboarding.Knowledge{{ID: "k1", Kind: "decision", Title: "Use OIDC", Body: "We use Keycloak.", Linked: true}},
		Extra:     "Prefer small commits.",
	}
}

func TestRender(t *testing.T) {
	out := bundle().Render(0)
	for _, want := range []string{
		"# WEB-3: Login", "**implement** stage", "branch `ballet/WEB-3-login`", "Implement the ticket with tests.",
		"Users sign in with OIDC.", "- [ ] OIDC sign-in works", "Review: agent; merge: auto",
		"### Epic WEB-1: Auth", "### Milestone WEB-0: MVP", "## Depends on WEB-2: Session store (done)",
		"> Added a Redis session store.\n> See pkg/session.", "## Knowledge (decision, linked to this ticket): Use OIDC",
		"We use Keycloak.", "## Additional instructions\n\nPrefer small commits.",
	} {
		assert.Contains(t, out, want)
	}
	assert.Less(t, strings.Index(out, "Depends on"), strings.Index(out, "Knowledge ("), "dependencies before knowledge")
}

func TestRender_StaysWithinTheLimit(t *testing.T) {
	b := bundle()
	for i := range 50 {
		b.Knowledge = append(b.Knowledge, onboarding.Knowledge{ID: "k", Kind: "document", Title: "Doc", Body: strings.Repeat("é long text ", 200)})
		_ = i
	}
	out := b.Render(8000)
	assert.LessOrEqual(t, len(out), 8000)
	assert.True(t, utf8.ValidString(out))
	assert.Contains(t, out, "- [ ] OIDC sign-in works", "the ticket always fits")
	assert.Contains(t, out, "Prefer small commits.", "and the additional instructions")
	assert.Contains(t, out, "omitted for size")

	b.Ticket.Description = strings.Repeat("x", 20000)
	out = b.Render(5000)
	assert.LessOrEqual(t, len(out), 5000)
	assert.Contains(t, out, "Prefer small commits.")
}
