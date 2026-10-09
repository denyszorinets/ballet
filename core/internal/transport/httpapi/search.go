package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/denyszorinets/ballet/core/internal/app"
)

type searchHitJSON struct {
	Kind         string  `json:"kind"`
	Ref          string  `json:"ref"`
	Title        string  `json:"title"`
	Snippet      string  `json:"snippet"`
	Organization string  `json:"organization,omitempty"`
	Project      string  `json:"project,omitempty"`
	Scope        string  `json:"scope,omitempty"`
	Score        float64 `json:"score"`
}

func registerSearch(mux *router, s *app.Search) {
	if s == nil {
		return
	}
	mux.handle("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		kind := q.Get("kind")
		if kind != "" && kind != "item" && kind != "skill" {
			writeError(w, fmt.Errorf("%w: kind must be item or skill", app.ErrInvalid))
			return
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		hits, err := s.Query(r.Context(), q.Get("q"), kind, q.Get("project"), limit)
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[searchHitJSON]{Items: make([]searchHitJSON, 0, len(hits))}
		for _, h := range hits {
			snippet := h.Body
			if utf8.RuneCountInString(snippet) > 240 {
				snippet = string([]rune(snippet)[:240]) + "…"
			}
			out.Items = append(out.Items, searchHitJSON{Kind: h.Kind, Ref: h.Ref, Title: h.Title, Snippet: snippet,
				Organization: h.Organization, Project: h.Project, Scope: h.Scope, Score: h.Score})
		}
		writeJSON(w, http.StatusOK, out)
	})
}
