package httpapi

import (
	"net/http"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

func registerQuestions(mux *router, qs *app.Questions) {
	mux.handle("POST /api/v1/questions/{question}/answer", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Answer string `json:"answer"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := qs.Answer(r.Context(), r.PathValue("question"), in.Answer)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toQuestionJSON(v.Question, v.TicketKey))
	})

	mux.handle("POST /api/v1/questions/{question}/chat", func(w http.ResponseWriter, r *http.Request) {
		s, err := qs.Chat(r.Context(), r.PathValue("question"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSessionJSON(s))
	})

	mux.handle("GET /api/v1/inbox", func(w http.ResponseWriter, r *http.Request) {
		list, err := qs.ListInbox(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[inboxJSON]{Items: make([]inboxJSON, 0, len(list))}
		for _, e := range list {
			out.Items = append(out.Items, inboxJSON{questionJSON: toQuestionJSON(e.Question, e.TicketKey),
				Customer: e.CustomerKey, Project: e.ProjectKey, TicketTitle: e.TicketTitle, TicketState: e.TicketState,
				BlockedBehind: e.BlockedBehind, Chat: e.Chat})
		}
		writeJSON(w, http.StatusOK, out)
	})
}

type inboxJSON struct {
	questionJSON
	Customer      string        `json:"customer"`
	Project       string        `json:"project"`
	TicketTitle   string        `json:"ticket_title"`
	TicketState   tracker.State `json:"ticket_state"`
	BlockedBehind int           `json:"blocked_behind"`
	Chat          string        `json:"chat,omitempty"`
}
