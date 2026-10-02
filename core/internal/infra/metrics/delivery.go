// Package metrics exports Core's delivery measurements to Prometheus.
package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/denyszorinets/ballet/core/internal/app"
)

// Delivery implements app.DeliveryObserver with Prometheus metrics and
// collects the current state (active flows, open questions, active runs)
// at scrape time.
type Delivery struct {
	stages     *prometheus.CounterVec
	flows      *prometheus.CounterVec
	leadTime   *prometheus.HistogramVec
	iterations *prometheus.HistogramVec
	waits      *prometheus.CounterVec
	runs       *prometheus.CounterVec
	runTime    *prometheus.HistogramVec
	raised     *prometheus.CounterVec
	answered   *prometheus.CounterVec
	answerTime *prometheus.HistogramVec
	tokens     *prometheus.CounterVec

	gauges        app.GaugeStore
	activeFlows   *prometheus.Desc
	openQuestions *prometheus.Desc
	activeRuns    *prometheus.Desc
}

var hours = []float64{60, 300, 900, 1800, 3600, 2 * 3600, 4 * 3600, 8 * 3600, 16 * 3600, 24 * 3600, 48 * 3600, 7 * 24 * 3600}

// NewDelivery registers delivery metrics on reg; gauges may be nil.
func NewDelivery(reg prometheus.Registerer, gauges app.GaugeStore) *Delivery {
	cp := []string{"customer", "project"}
	d := &Delivery{
		stages: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ballet_stages_finished_total",
			Help: "Pipeline stages finished, by stage and outcome (done, failed, blocked)."},
			append(cp, "stage", "outcome")),
		flows: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ballet_flows_finished_total",
			Help: "Ticket pipelines finished, by status (done, failed, stopped)."}, append(cp, "status")),
		leadTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ballet_flow_lead_time_seconds",
			Help: "Time from a pipeline's start to its end, by status.", Buckets: hours}, append(cp, "status")),
		iterations: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ballet_flow_iterations",
			Help: "Loops a pipeline went through before it ended.", Buckets: []float64{0, 1, 2, 3, 5, 8, 13, 20}}, cp),
		waits: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ballet_flow_waits_total",
			Help: "Times pipelines started waiting, by reason (approval, checks, review, merge, question, pause, budget)."},
			append(cp, "reason")),
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ballet_runs_finished_total",
			Help: "Agent runs finished, by stage and status."}, append(cp, "stage", "status")),
		runTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ballet_run_duration_seconds",
			Help: "Duration of agent sessions, by stage.", Buckets: []float64{30, 60, 120, 300, 600, 1200, 1800, 3600, 7200}},
			append(cp, "stage")),
		raised: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ballet_questions_raised_total",
			Help: "Questions raised, by whether they block the ticket."}, append(cp, "blocking")),
		answered: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ballet_questions_answered_total",
			Help: "Questions answered, by who answered (planner, human)."}, append(cp, "by")),
		answerTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ballet_question_answer_seconds",
			Help: "Time from a question to its answer, by who answered.", Buckets: hours}, append(cp, "by")),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ballet_usage_tokens_total",
			Help: "LLM tokens used, as the gateway reported them, by type (input, output, cache_read, cache_write)."},
			append(cp, "type")),
		gauges: gauges,
		activeFlows: prometheus.NewDesc("ballet_flows_active", "Pipelines in progress, by status and what they wait for.",
			append(cp, "status", "waiting"), nil),
		openQuestions: prometheus.NewDesc("ballet_questions_open", "Open questions, by route (planner, human).",
			append(cp, "route"), nil),
		activeRuns: prometheus.NewDesc("ballet_runs_active", "Queued and active agent runs, by status.",
			append(cp, "status"), nil),
	}
	reg.MustRegister(d.stages, d.flows, d.leadTime, d.iterations, d.waits, d.runs, d.runTime, d.raised, d.answered,
		d.answerTime, d.tokens)
	if gauges != nil {
		reg.MustRegister(gaugeCollector{d})
	}
	return d
}

// StageFinished implements app.DeliveryObserver.
func (d *Delivery) StageFinished(customer, project, stage, outcome string) {
	d.stages.WithLabelValues(customer, project, stage, outcome).Inc()
}

// FlowFinished implements app.DeliveryObserver.
func (d *Delivery) FlowFinished(customer, project, status string, lead time.Duration, iterations int) {
	d.flows.WithLabelValues(customer, project, status).Inc()
	d.leadTime.WithLabelValues(customer, project, status).Observe(lead.Seconds())
	if status == "done" {
		d.iterations.WithLabelValues(customer, project).Observe(float64(iterations))
	}
}

// FlowWaiting implements app.DeliveryObserver.
func (d *Delivery) FlowWaiting(customer, project, reason string) {
	d.waits.WithLabelValues(customer, project, reason).Inc()
}

// RunFinished implements app.DeliveryObserver.
func (d *Delivery) RunFinished(customer, project, stage, status string, took time.Duration) {
	d.runs.WithLabelValues(customer, project, stage, status).Inc()
	if took > 0 {
		d.runTime.WithLabelValues(customer, project, stage).Observe(took.Seconds())
	}
}

// QuestionRaised implements app.DeliveryObserver.
func (d *Delivery) QuestionRaised(customer, project string, blocking bool) {
	b := "false"
	if blocking {
		b = "true"
	}
	d.raised.WithLabelValues(customer, project, b).Inc()
}

// QuestionAnswered implements app.DeliveryObserver.
func (d *Delivery) QuestionAnswered(customer, project, by string, wait time.Duration) {
	d.answered.WithLabelValues(customer, project, by).Inc()
	d.answerTime.WithLabelValues(customer, project, by).Observe(wait.Seconds())
}

// Usage counts the tokens of an ingested usage record (Usage.Observe).
func (d *Delivery) Usage(r app.UsageInput) {
	for typ, n := range map[string]int64{"input": r.InputTokens, "output": r.OutputTokens, "cache_read": r.CacheRead,
		"cache_write": r.CacheWrite} {
		if n > 0 {
			d.tokens.WithLabelValues(r.Customer, r.Project, typ).Add(float64(n))
		}
	}
}

// LeadTime returns the lead time histogram (tests).
func (d *Delivery) LeadTime() prometheus.Collector { return d.leadTime }

type gaugeCollector struct{ d *Delivery }

func (g gaugeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- g.d.activeFlows
	ch <- g.d.openQuestions
	ch <- g.d.activeRuns
}

func (g gaugeCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := g.d.gauges.DeliveryGauges(ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(g.d.activeFlows, err)
		return
	}
	for _, r := range s.Flows {
		ch <- prometheus.MustNewConstMetric(g.d.activeFlows, prometheus.GaugeValue, float64(r.Count), r.Customer, r.Project,
			r.State, r.Detail)
	}
	for _, r := range s.Questions {
		ch <- prometheus.MustNewConstMetric(g.d.openQuestions, prometheus.GaugeValue, float64(r.Count), r.Customer,
			r.Project, r.State)
	}
	for _, r := range s.Runs {
		ch <- prometheus.MustNewConstMetric(g.d.activeRuns, prometheus.GaugeValue, float64(r.Count), r.Customer, r.Project,
			r.State)
	}
}
