package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
)

type pullRequestJSON struct {
	Ticket    string       `json:"ticket"`
	Forge     string       `json:"forge"`
	Number    int          `json:"number"`
	URL       string       `json:"url"`
	Title     string       `json:"title"`
	Head      string       `json:"head"`
	Base      string       `json:"base"`
	HeadSHA   string       `json:"head_sha,omitempty"`
	State     forge.State  `json:"state"`
	Draft     bool         `json:"draft"`
	Mergeable *bool        `json:"mergeable,omitempty"`
	Checks    forge.Checks `json:"checks"`
	Review    forge.Review `json:"review"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func toPullRequestJSON(v app.PRView) pullRequestJSON {
	return pullRequestJSON{Ticket: v.TicketKey, Forge: v.Forge, Number: v.Number, URL: v.URL, Title: v.Title, Head: v.Head,
		Base: v.Base, HeadSHA: v.HeadSHA, State: v.State, Draft: v.Draft, Mergeable: v.Mergeable, Checks: v.Checks,
		Review: v.Review, UpdatedAt: v.UpdatedAt}
}

func registerPullRequests(mux *router, ps *app.PullRequests) {
	serve := func(f func(r *http.Request) (app.PRView, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			v, err := f(r)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, toPullRequestJSON(v))
		}
	}
	mux.handle("GET /api/v1/items/{item}/pull-request", serve(func(r *http.Request) (app.PRView, error) {
		return ps.Get(r.Context(), r.PathValue("item"))
	}))
	mux.handle("POST /api/v1/items/{item}/pull-request", serve(func(r *http.Request) (app.PRView, error) {
		return ps.Open(r.Context(), r.PathValue("item"))
	}))
	mux.handle("POST /api/v1/items/{item}/pull-request/refresh", serve(func(r *http.Request) (app.PRView, error) {
		return ps.Refresh(r.Context(), r.PathValue("item"))
	}))
	mux.handle("POST /api/v1/items/{item}/pull-request/merge", serve(func(r *http.Request) (app.PRView, error) {
		return ps.Merge(r.Context(), r.PathValue("item"))
	}))
}
