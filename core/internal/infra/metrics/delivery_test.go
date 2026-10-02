package metrics_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/infra/metrics"
)

type gauges struct{}

func (gauges) DeliveryGauges(context.Context) (app.DeliveryGauges, error) {
	return app.DeliveryGauges{
		Flows:     []app.GaugeRow{{Customer: "acme", Project: "WEB", State: "waiting", Detail: "question", Count: 2}},
		Questions: []app.GaugeRow{{Customer: "acme", Project: "WEB", State: "human", Count: 3}},
	}, nil
}

func TestDelivery_ExportsCountersAndCurrentState(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := metrics.NewDelivery(reg, gauges{})
	d.StageFinished("acme", "WEB", "review", "failed")
	d.FlowFinished("acme", "WEB", "done", 3*time.Hour, 1)
	d.QuestionAnswered("acme", "WEB", "planner", time.Minute)
	d.Usage(app.UsageInput{Customer: "acme", Project: "WEB", InputTokens: 10, OutputTokens: 5})

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP ballet_flows_active Pipelines in progress, by status and what they wait for.
# TYPE ballet_flows_active gauge
ballet_flows_active{customer="acme",project="WEB",status="waiting",waiting="question"} 2
# HELP ballet_questions_open Open questions, by route (planner, human).
# TYPE ballet_questions_open gauge
ballet_questions_open{customer="acme",project="WEB",route="human"} 3
# HELP ballet_stages_finished_total Pipeline stages finished, by stage and outcome (done, failed, blocked).
# TYPE ballet_stages_finished_total counter
ballet_stages_finished_total{customer="acme",outcome="failed",project="WEB",stage="review"} 1
# HELP ballet_usage_tokens_total LLM tokens used, as the gateway reported them, by type (input, output, cache_read, cache_write).
# TYPE ballet_usage_tokens_total counter
ballet_usage_tokens_total{customer="acme",project="WEB",type="input"} 10
ballet_usage_tokens_total{customer="acme",project="WEB",type="output"} 5
`), "ballet_flows_active", "ballet_questions_open", "ballet_stages_finished_total", "ballet_usage_tokens_total"))
	assert.Equal(t, 1, testutil.CollectAndCount(d.LeadTime(), "ballet_flow_lead_time_seconds"))
}
