package internalapi

import (
	"errors"
	"net/http"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// BudgetVerdict tells the gateway whether to let LLM calls through.
type BudgetVerdict struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// RegisterBudgets adds GET /internal/v1/budget/check
// (?organization=<key>&project=<key>&ticket=<key>) for holders of
// credentials.read (the gateway, before forwarding a call).
func RegisterBudgets(r *Router, bs *app.Budgets) {
	r.Handle("GET /internal/v1/budget/check", runtoken.CapCredentialsRead, func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		ex, err := bs.CheckKeys(req.Context(), q.Get("organization"), q.Get("project"), q.Get("ticket"))
		if errors.Is(err, app.ErrNotFound) {
			WriteError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		if ex != nil {
			WriteJSON(w, http.StatusOK, BudgetVerdict{Reason: ex.Reason()})
			return
		}
		WriteJSON(w, http.StatusOK, BudgetVerdict{Allowed: true})
	})
}
