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
	// Assumptions.
	Ticket        string        `json:"ticket,omitempty"`
	TicketTitle   string        `json:"ticket_title,omitempty"`
	Review        report.Review `json:"review,omitempty"`
	ReviewComment string        `json:"review_comment,omitempty"`
	ReviewedBy    string        `json:"reviewed_by,omitempty"`
	ReviewedAt    *time.Time    `json:"reviewed_at,omitempty"`
	FollowUp      string        `json:"follow_up,omitempty"`
}

func toReportJSON(x report.Report) reportJSON {
	return reportJSON{ID: x.ID, Run: x.RunID, Kind: x.Kind, Outcome: x.Outcome, Text: x.Text, Detail: x.Detail,
		CreatedAt: x.CreatedAt, Review: x.Review, ReviewComment: x.ReviewComment, ReviewedBy: x.ReviewedBy,
		ReviewedAt: timePtr(x.ReviewedAt), FollowUp: x.FollowUp}
}

type questionJSON struct {
	ID         string                `json:"id"`
	Ticket     string                `json:"ticket,omitempty"`
	Run        string                `json:"run,omitempty"`
	Text       string                `json:"text"`
	Context    string                `json:"context,omitempty"`
	Blocking   bool                  `json:"blocking"`
	Status     report.QuestionStatus `json:"status"`
	Route      string                `json:"route,omitempty"`
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
			out.Items = append(out.Items, toReportJSON(x))
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
			out.Items = append(out.Items, toQuestionJSON(q, r.PathValue("item")))
		}
		writeJSON(w, http.StatusOK, out)
	})
}

func toQuestionJSON(q report.Question, ticketKey string) questionJSON {
	return questionJSON{ID: q.ID, Ticket: ticketKey, Run: q.RunID, Text: q.Text, Context: q.Context, Blocking: q.Blocking,
		Status: q.Status, Route: q.Route, Answer: q.Answer, AnsweredBy: q.AnsweredBy, CreatedAt: q.CreatedAt,
		AnsweredAt: timePtr(q.AnsweredAt)}
}
