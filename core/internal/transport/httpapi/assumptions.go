package httpapi

import (
	"net/http"

	"github.com/denyszorinets/ballet/core/internal/app"
)

func registerAssumptions(mux *router, as *app.Assumptions) {
	mux.handle("GET /api/v1/projects/{project}/assumptions", func(w http.ResponseWriter, r *http.Request) {
		list, err := as.List(r.Context(), r.PathValue("project"), r.URL.Query().Get("review"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[reportJSON]{Items: make([]reportJSON, 0, len(list))}
		for _, v := range list {
			out.Items = append(out.Items, toAssumptionJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})
	review := func(confirm bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Comment string `json:"comment"`
			}
			if err := decode(r, &in); err != nil {
				writeError(w, err)
				return
			}
			v, err := as.Review(r.Context(), r.PathValue("report"), confirm, in.Comment)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, toAssumptionJSON(v))
		}
	}
	mux.handle("POST /api/v1/assumptions/{report}/confirm", review(true))
	mux.handle("POST /api/v1/assumptions/{report}/reject", review(false))
}

func toAssumptionJSON(v app.AssumptionView) reportJSON {
	out := toReportJSON(v.Report)
	out.Ticket, out.TicketTitle = v.TicketKey, v.TicketTitle
	return out
}
