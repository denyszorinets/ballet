package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/report"
)

type reportJSON struct {
	ID        string         `json:"id"`
	Run       string         `json:"run"`
	Kind      report.Kind    `json:"kind"`
	Outcome   report.Outcome `json:"outcome,omitempty"`
	Text      string         `json:"text"`
	Detail    string         `json:"detail,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type questionJSON struct {
	ID         string                `json:"id"`
	Run        string                `json:"run,omitempty"`
	Text       string                `json:"text"`
	Context    string                `json:"context,omitempty"`
	Blocking   bool                  `json:"blocking"`
	Status     report.QuestionStatus `json:"status"`
	Answer     string                `json:"answer,omitempty"`
	AnsweredBy string                `json:"answered_by,omitempty"`
	CreatedAt  time.Time             `json:"created_at"`
	AnsweredAt *time.Time            `json:"answered_at,omitempty"`
}

func registerReports(mux *router, at *app.AgentTracker) {
	mux.handle("GET /api/v1/items/{item}/reports", func(w http.ResponseWriter, r *http.Request) {
		reports, _, err := at.TicketReports(r.Context(), r.PathValue("item"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[reportJSON]{Items: make([]reportJSON, 0, len(reports))}
		for _, x := range reports {
			out.Items = append(out.Items, reportJSON{ID: x.ID, Run: x.RunID, Kind: x.Kind, Outcome: x.Outcome, Text: x.Text,
				Detail: x.Detail, CreatedAt: x.CreatedAt})
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("GET /api/v1/items/{item}/questions", func(w http.ResponseWriter, r *http.Request) {
		_, questions, err := at.TicketReports(r.Context(), r.PathValue("item"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[questionJSON]{Items: make([]questionJSON, 0, len(questions))}
		for _, q := range questions {
			out.Items = append(out.Items, questionJSON{ID: q.ID, Run: q.RunID, Text: q.Text, Context: q.Context,
				Blocking: q.Blocking, Status: q.Status, Answer: q.Answer, AnsweredBy: q.AnsweredBy, CreatedAt: q.CreatedAt,
				AnsweredAt: timePtr(q.AnsweredAt)})
		}
		writeJSON(w, http.StatusOK, out)
	})
}
