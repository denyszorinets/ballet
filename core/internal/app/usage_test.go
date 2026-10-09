package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
)

func TestUsage_IngestAndReport(t *testing.T) {
	env := newRBACEnv(t)
	seed(t, env)
	u := &app.Usage{Store: env.store, Tenancy: env.store, Authz: env.rbac}
	now := time.Now()
	rec := func(project, ticket, model string, in, out int64) app.UsageInput {
		return app.UsageInput{OccurredAt: now, Organization: "acme", Project: project, Ticket: ticket, Model: model,
			Status: 200, InputTokens: in, OutputTokens: out}
	}

	stored, skipped, err := u.Ingest(t.Context(), []app.UsageInput{
		rec("WEB", "WEB-1", "opus", 100, 10),
		rec("WEB", "WEB-1", "haiku", 50, 5),
		rec("WEB", "WEB-2", "opus", 10, 1),
		rec("APP", "APP-1", "opus", 999, 9),
		{Organization: "globex", Project: "WEB", Ticket: "WEB-1", InputTokens: 1}, // wrong organization for WEB
		{Organization: "acme", Project: "NOPE", InputTokens: 1},
	})
	require.NoError(t, err)
	assert.Equal(t, 4, stored)
	assert.Equal(t, 2, skipped)

	bob := user(t, "bob", "acme-devs")
	byTicket, err := u.Report(bob, "WEB", "ticket", time.Time{}, "")
	require.NoError(t, err)
	require.Len(t, byTicket.Groups, 2)
	assert.Equal(t, app.UsageTotals{Key: "WEB-1", Requests: 2, InputTokens: 150, OutputTokens: 15}, byTicket.Groups[0])
	assert.Equal(t, int64(160), byTicket.Total.InputTokens, "other projects excluded")

	byModel, err := u.Report(bob, "WEB", "model", time.Time{}, "")
	require.NoError(t, err)
	assert.Equal(t, "opus", byModel.Groups[0].Key)

	byRun, err := u.Report(bob, "WEB", "run", time.Time{}, "WEB-1")
	require.NoError(t, err)
	require.Len(t, byRun.Groups, 1)
	assert.Equal(t, int64(150), byRun.Total.InputTokens, "only WEB-1, grouped by its caller")
	later, err := u.Report(bob, "WEB", "ticket", now.Add(time.Minute), "")
	require.NoError(t, err)
	assert.Empty(t, later.Groups)

	_, err = u.Report(bob, "WEB", "day", time.Time{}, "")
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = u.Report(user(t, "eve"), "WEB", "ticket", time.Time{}, "")
	assert.ErrorIs(t, err, app.ErrForbidden)
}
