package httpapi

import (
	"net/http"

	"github.com/denyszorinets/ballet/core/internal/app"
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
}
