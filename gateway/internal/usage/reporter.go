package usage

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Poster delivers records to Core (core.Client).
type Poster interface {
	Post(ctx context.Context, path string, body any) error
}

// maxPending bounds records kept while Core is unreachable.
const maxPending = 100_000

// Reporter counts usage in Prometheus metrics and delivers records to
// Core in batches, retrying until Core accepts them.
type Reporter struct {
	core     Poster
	interval time.Duration
	logger   *slog.Logger
	tokens   *prometheus.CounterVec
	requests *prometheus.CounterVec

	mu      sync.Mutex
	pending []Record
}

// NewReporter registers the metrics on reg.
func NewReporter(core Poster, reg prometheus.Registerer, interval time.Duration, logger *slog.Logger) *Reporter {
	r := &Reporter{
		core: core, interval: interval, logger: logger,
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ballet_llm_tokens_total", Help: "LLM tokens by organization, project, model and token type.",
		}, []string{"organization", "project", "model", "token_type"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ballet_llm_requests_total", Help: "LLM requests by organization, project, model and HTTP status.",
		}, []string{"organization", "project", "model", "status"}),
	}
	reg.MustRegister(r.tokens, r.requests)
	return r
}

// Sink records one request.
func (r *Reporter) Sink(rec Record) {
	r.requests.WithLabelValues(rec.Organization, rec.Project, rec.Model, strconv.Itoa(rec.Status)).Inc()
	for typ, n := range map[string]int64{
		"input": rec.InputTokens, "output": rec.OutputTokens, "cache_read": rec.CacheRead, "cache_write": rec.CacheWrite,
	} {
		if n > 0 {
			r.tokens.WithLabelValues(rec.Organization, rec.Project, rec.Model, typ).Add(float64(n))
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) >= maxPending {
		r.logger.Error("usage backlog full; dropping oldest record")
		r.pending = r.pending[1:]
	}
	r.pending = append(r.pending, rec)
}

// Run flushes every interval until ctx is cancelled, then flushes once more.
func (r *Reporter) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			r.Flush(flushCtx)
			cancel()
			return
		case <-t.C:
			r.Flush(ctx)
		}
	}
}

// Flush delivers pending records; on failure they stay pending.
func (r *Reporter) Flush(ctx context.Context) {
	r.mu.Lock()
	batch := r.pending
	r.pending = nil
	r.mu.Unlock()
	for len(batch) > 0 {
		n := min(len(batch), 500)
		if err := r.core.Post(ctx, "/internal/v1/usage", map[string]any{"records": batch[:n]}); err != nil {
			r.logger.Warn("delivering usage to core failed; will retry", "error", err, "records", len(batch))
			r.mu.Lock()
			r.pending = append(batch, r.pending...)
			r.mu.Unlock()
			return
		}
		batch = batch[n:]
	}
}
