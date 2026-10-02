package app_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
)

type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fmt.Sprintf(format, args...))
}

func (r *recorder) StageFinished(c, p, stage, outcome string) {
	r.add("stage %s/%s %s %s", c, p, stage, outcome)
}
func (r *recorder) FlowFinished(c, p, status string, lead time.Duration, iterations int) {
	r.add("flow %s/%s %s lead>0=%v iterations=%d", c, p, status, lead > 0, iterations)
}
func (r *recorder) FlowWaiting(c, p, reason string) { r.add("wait %s/%s %s", c, p, reason) }
func (r *recorder) RunFinished(c, p, stage, status string, took time.Duration) {
	r.add("run %s/%s %s %s", c, p, stage, status)
}
func (r *recorder) QuestionRaised(c, p string, blocking bool) {
	r.add("raised %s/%s %v", c, p, blocking)
}
func (r *recorder) QuestionAnswered(c, p, by string, wait time.Duration) {
	r.add("answered %s/%s %s", c, p, by)
}

func TestDelivery_MeasuresFromTheEventLog(t *testing.T) {
	e := newFlows(t, func(stage string, n int) report.Outcome {
		if stage == "review" && n == 1 {
			return report.OutcomeFailed
		}
		return report.OutcomeDone
	})
	e.forge.checks = forge.ChecksSuccess
	tk := e.ticket(t, auto)
	_, err := e.flows.Start(user(t, "dave", "acme-admins"), tk.Key)
	require.NoError(t, err)
	e.waitFlow(t, tk.Key, func(f app.FlowView) bool { return f.Status == app.FlowDone })

	rec := &recorder{}
	d := &app.Delivery{Log: e.st, Flows: e.st, Runs: e.st, Reports: e.st, Tenancy: e.st, Observer: rec}
	cursor := d.Observe(t.Context(), 0)
	assert.Positive(t, cursor)
	assert.Subset(t, rec.calls, []string{
		"stage acme/WEB implement done",
		"stage acme/WEB review failed",
		"stage acme/WEB verify done",
		"stage acme/WEB integrate done",
		"run acme/WEB implement succeeded",
		"run acme/WEB review succeeded",
		"flow acme/WEB done lead>0=true iterations=1",
	})
	assert.Equal(t, cursor, d.Observe(t.Context(), cursor), "nothing new")
}
