package internalapi

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

type usageRecordJSON struct {
	OccurredAt   time.Time `json:"occurred_at"`
	Run          string    `json:"run"`
	Customer     string    `json:"customer"`
	Project      string    `json:"project"`
	Ticket       string    `json:"ticket"`
	Model        string    `json:"model"`
	Status       int       `json:"status"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CacheRead    int64     `json:"cache_read_tokens"`
	CacheWrite   int64     `json:"cache_write_tokens"`
}

// RegisterUsage adds POST /internal/v1/usage ({"records": [...]}) for
// holders of usage.write (the gateway).
func RegisterUsage(r *Router, u *app.Usage) {
	r.Handle("POST /internal/v1/usage", runtoken.CapUsageWrite, func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Records []usageRecordJSON `json:"records"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 16<<20)).Decode(&body); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_argument", "invalid body: "+err.Error())
			return
		}
		in := make([]app.UsageInput, 0, len(body.Records))
		for _, r := range body.Records {
			in = append(in, app.UsageInput{
				OccurredAt: r.OccurredAt, Customer: r.Customer, Project: r.Project, Ticket: r.Ticket, Run: r.Run,
				Model: r.Model, Status: r.Status, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
				CacheRead: r.CacheRead, CacheWrite: r.CacheWrite,
			})
		}
		stored, skipped, err := u.Ingest(req.Context(), in)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int{"stored": stored, "skipped": skipped})
	})
}
