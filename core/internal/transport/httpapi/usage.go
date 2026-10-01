package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
)

type usageTotalsJSON struct {
	Key          string `json:"key,omitempty"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CacheRead    int64  `json:"cache_read_tokens"`
	CacheWrite   int64  `json:"cache_write_tokens"`
}

func toUsageJSON(t app.UsageTotals) usageTotalsJSON {
	return usageTotalsJSON{Key: t.Key, Requests: t.Requests, InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
		CacheRead: t.CacheRead, CacheWrite: t.CacheWrite}
}

func registerUsage(mux *router, u *app.Usage) {
	mux.handle("GET /api/v1/projects/{project}/usage", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		groupBy := q.Get("group_by")
		if groupBy == "" {
			groupBy = "ticket"
		}
		var since time.Time
		if s := q.Get("since"); s != "" {
			var err error
			if since, err = time.Parse(time.RFC3339, s); err != nil {
				writeError(w, fmt.Errorf("%w: since must be RFC 3339", app.ErrInvalid))
				return
			}
		}
		rep, err := u.Report(r.Context(), r.PathValue("project"), groupBy, since)
		if err != nil {
			writeError(w, err)
			return
		}
		out := struct {
			GroupBy string            `json:"group_by"`
			Items   []usageTotalsJSON `json:"items"`
			Total   usageTotalsJSON   `json:"total"`
		}{GroupBy: groupBy, Items: make([]usageTotalsJSON, 0, len(rep.Groups)), Total: toUsageJSON(rep.Total)}
		for _, g := range rep.Groups {
			out.Items = append(out.Items, toUsageJSON(g))
		}
		writeJSON(w, http.StatusOK, out)
	})
}
