package usage_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/gateway/internal/usage"
)

type fakeCore struct {
	mu      sync.Mutex
	fail    bool
	batches [][]usage.Record
}

func (f *fakeCore) Post(_ context.Context, _ string, body any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("core down")
	}
	f.batches = append(f.batches, body.(map[string]any)["records"].([]usage.Record))
	return nil
}

func TestReporter_CountsMetricsAndRetriesDelivery(t *testing.T) {
	core := &fakeCore{fail: true}
	reg := prometheus.NewRegistry()
	r := usage.NewReporter(core, reg, 0, slog.New(slog.DiscardHandler))
	rec := usage.Record{Organization: "acme", Project: "WEB", Ticket: "WEB-1", Model: "m", Status: 200, InputTokens: 10, OutputTokens: 4}

	r.Sink(rec)
	r.Sink(rec)
	r.Flush(t.Context())
	assert.Empty(t, core.batches, "nothing delivered while core is down")

	core.fail = false
	r.Flush(t.Context())
	assert.Len(t, core.batches, 1)
	assert.Len(t, core.batches[0], 2, "records kept and delivered after recovery")
	r.Flush(t.Context())
	assert.Len(t, core.batches, 1, "nothing delivered twice")

	assert.Equal(t, float64(20), testutil.ToFloat64(prometheusCounter(reg, t, "input")))
	assert.Equal(t, 1, testutil.CollectAndCount(reg, "ballet_llm_requests_total"))
}

func prometheusCounter(reg *prometheus.Registry, t *testing.T, typ string) prometheus.Collector {
	t.Helper()
	mfs, err := reg.Gather()
	assert.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "ballet_llm_tokens_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "token_type" && l.GetValue() == typ {
					return prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "x"}, func() float64 { return m.GetCounter().GetValue() })
				}
			}
		}
	}
	t.Fatalf("no %s counter", typ)
	return nil
}
