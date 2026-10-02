package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
)

type digestTicketJSON struct {
	Key    string    `json:"key"`
	Title  string    `json:"title,omitempty"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"`
}

type digestQuestionJSON struct {
	Ticket   string    `json:"ticket"`
	Text     string    `json:"text"`
	Blocking bool      `json:"blocking"`
	Route    string    `json:"route,omitempty"`
	Since    time.Time `json:"since"`
}

type digestJSON struct {
	Project           string               `json:"project"`
	Since             time.Time            `json:"since"`
	Until             time.Time            `json:"until"`
	Done              []digestTicketJSON   `json:"done"`
	Failed            []digestTicketJSON   `json:"failed"`
	Started           []digestTicketJSON   `json:"started"`
	Merged            []digestTicketJSON   `json:"merged"`
	Waiting           []digestTicketJSON   `json:"waiting"`
	QuestionsRaised   int                  `json:"questions_raised"`
	AnsweredByPlanner int                  `json:"answered_by_planner"`
	AnsweredByHuman   int                  `json:"answered_by_human"`
	OpenQuestions     []digestQuestionJSON `json:"open_questions"`
	Assumptions       []digestTicketJSON   `json:"assumptions"`
	Proposals         int                  `json:"proposals"`
	Runs              map[string]int       `json:"runs"`
	Tokens            int64                `json:"tokens"`
	Interventions     []string             `json:"interventions"`
	Markdown          string               `json:"markdown"`
}

func digestTickets(ts []app.DigestTicket) []digestTicketJSON {
	out := make([]digestTicketJSON, 0, len(ts))
	for _, t := range ts {
		out = append(out, digestTicketJSON{Key: t.Key, Title: t.Title, At: t.At, Detail: t.Detail})
	}
	return out
}

func registerDigests(mux *router, ds *app.Digests) {
	mux.handle("GET /api/v1/projects/{project}/digest", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var since, until time.Time
		for name, dst := range map[string]*time.Time{"since": &since, "until": &until} {
			if v := q.Get(name); v != "" {
				t, err := time.Parse(time.RFC3339, v)
				if err != nil {
					writeError(w, fmt.Errorf("%w: %s must be an RFC 3339 time", app.ErrInvalid, name))
					return
				}
				*dst = t
			}
		}
		if since.IsZero() {
			end := until
			if end.IsZero() {
				end = time.Now()
			}
			since = end.Add(-24 * time.Hour)
		}
		d, err := ds.Project(r.Context(), r.PathValue("project"), since, until)
		if err != nil {
			writeError(w, err)
			return
		}
		out := digestJSON{Project: d.ProjectKey, Since: d.Since, Until: d.Until, Done: digestTickets(d.Done),
			Failed: digestTickets(d.Failed), Started: digestTickets(d.Started), Merged: digestTickets(d.Merged),
			Waiting: digestTickets(d.Waiting), QuestionsRaised: d.QuestionsRaised, AnsweredByPlanner: d.AnsweredByPlanner,
			AnsweredByHuman: d.AnsweredByHuman, OpenQuestions: []digestQuestionJSON{}, Assumptions: digestTickets(d.Assumptions),
			Proposals: d.Proposals, Runs: d.Runs, Tokens: d.Tokens, Interventions: append([]string{}, d.Interventions...),
			Markdown: d.Markdown()}
		for _, oq := range d.OpenQuestions {
			out.OpenQuestions = append(out.OpenQuestions, digestQuestionJSON{Ticket: oq.Ticket, Text: oq.Text,
				Blocking: oq.Blocking, Route: oq.Route, Since: oq.Since})
		}
		writeJSON(w, http.StatusOK, out)
	})
}
