package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
)

func TestDigest_SummarizesAPeriod(t *testing.T) {
	e := newFlows(t, func(stage string, n int) report.Outcome {
		if stage == "review" && n == 1 {
			return report.OutcomeFailed
		}
		return report.OutcomeDone
	})
	e.forge.checks = forge.ChecksSuccess
	start := time.Now()
	dave := user(t, "dave", "acme-admins")
	done := e.ticket(t, auto)
	_, err := e.flows.Start(dave, done.Key)
	require.NoError(t, err)
	e.waitFlow(t, done.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })

	ds := &app.Digests{Store: e.st, Tenancy: e.st, Authz: e.flows.Authz, Now: time.Now}
	_, err = ds.Project(user(t, "eve"), "WEB", start, time.Time{})
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = ds.Project(dave, "WEB", time.Now(), start)
	assert.ErrorIs(t, err, app.ErrInvalid)

	d, err := ds.Project(user(t, "carol", "acme-viewers"), "WEB", start.Add(-time.Second), time.Time{})
	require.NoError(t, err)
	require.Len(t, d.Done, 1)
	assert.Equal(t, done.Key, d.Done[0].Key)
	assert.Equal(t, "Login", d.Done[0].Title)
	require.Len(t, d.Started, 1)
	assert.Equal(t, 5, d.Runs["succeeded"], "implement, review, implement, review, verify")
	assert.Empty(t, d.Failed)
	md := d.Markdown()
	assert.Contains(t, md, "# Digest of WEB")
	assert.Contains(t, md, "**1** tickets done, **0** failed, **1** started")
	assert.Contains(t, md, "**5** agent sessions (5 succeeded)")
	assert.Contains(t, md, "## Done\n\n- "+done.Key+" Login")

	// A period before anything happened is empty.
	empty, err := ds.Project(dave, "WEB", start.Add(-time.Hour), start.Add(-time.Minute))
	require.NoError(t, err)
	assert.Empty(t, empty.Done)
	assert.Empty(t, empty.Runs)
}
