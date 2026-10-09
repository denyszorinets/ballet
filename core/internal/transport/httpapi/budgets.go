package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
)

type budgetJSON struct {
	TicketTokens int64      `json:"ticket_tokens"`
	DailyTokens  int64      `json:"daily_tokens"`
	UsedToday    int64      `json:"used_today"`
	UpdatedBy    string     `json:"updated_by,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	Version      int64      `json:"version"`
}

func toBudgetJSON(s app.BudgetStatus) budgetJSON {
	return budgetJSON{TicketTokens: s.TicketTokens, DailyTokens: s.DailyTokens, UsedToday: s.UsedToday,
		UpdatedBy: s.UpdatedBy, UpdatedAt: timePtr(s.UpdatedAt), Version: s.Version}
}

func registerBudgets(mux *router, bs *app.Budgets) {
	keys := func(r *http.Request) (string, string) { return r.PathValue("organization"), r.PathValue("project") }
	get := func(w http.ResponseWriter, r *http.Request) {
		c, p := keys(r)
		s, err := bs.Get(r.Context(), c, p)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toBudgetJSON(s))
	}
	put := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TicketTokens int64 `json:"ticket_tokens"`
			DailyTokens  int64 `json:"daily_tokens"`
			Version      int64 `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		c, p := keys(r)
		s, err := bs.Set(r.Context(), c, p, in.TicketTokens, in.DailyTokens, in.Version)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toBudgetJSON(s))
	}
	mux.handle("GET /api/v1/organizations/{organization}/budget", get)
	mux.handle("PUT /api/v1/organizations/{organization}/budget", put)
	mux.handle("GET /api/v1/projects/{project}/budget", get)
	mux.handle("PUT /api/v1/projects/{project}/budget", put)
}
