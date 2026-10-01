package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
)

type changesetJSON struct {
	ID         string             `json:"id"`
	Project    string             `json:"project"`
	Title      string             `json:"title"`
	Summary    string             `json:"summary"`
	Status     changeset.Status   `json:"status"`
	Operations []changeset.Op     `json:"operations"`
	ProposedBy event.Actor        `json:"proposed_by"`
	CreatedAt  time.Time          `json:"created_at"`
	DecidedBy  *event.Actor       `json:"decided_by,omitempty"`
	DecidedAt  *time.Time         `json:"decided_at,omitempty"`
	Approved   []int              `json:"approved"`
	Results    []changeset.Result `json:"results"`
	Version    int64              `json:"version"`
}

func toChangesetJSON(v app.ChangesetView) changesetJSON {
	j := changesetJSON{
		ID: v.ID, Project: v.ProjectKey, Title: v.Title, Summary: v.Summary, Status: v.Status, Operations: v.Ops,
		ProposedBy: v.ProposedBy, CreatedAt: v.CreatedAt, Approved: v.Approved, Results: v.Results, Version: v.Version,
	}
	if j.Approved == nil {
		j.Approved = []int{}
	}
	if j.Results == nil {
		j.Results = []changeset.Result{}
	}
	if !v.DecidedAt.IsZero() {
		j.DecidedBy, j.DecidedAt = &v.DecidedBy, &v.DecidedAt
	}
	return j
}

func registerChangesets(mux *router, cs *app.Changesets) {
	mux.handle("GET /api/v1/projects/{project}/changesets", func(w http.ResponseWriter, r *http.Request) {
		list, err := cs.List(r.Context(), r.PathValue("project"), changeset.Status(r.URL.Query().Get("status")))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[changesetJSON]{Items: make([]changesetJSON, 0, len(list))}
		for _, v := range list {
			out.Items = append(out.Items, toChangesetJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("POST /api/v1/projects/{project}/changesets", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title      string         `json:"title"`
			Summary    string         `json:"summary"`
			Operations []changeset.Op `json:"operations"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := cs.Propose(r.Context(), app.ProposeInput{
			ProjectKey: r.PathValue("project"), Title: in.Title, Summary: in.Summary, Ops: in.Operations,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetJSON(v))
	})

	mux.handle("GET /api/v1/changesets/{changeset}", func(w http.ResponseWriter, r *http.Request) {
		v, err := cs.Get(r.Context(), r.PathValue("changeset"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetJSON(v))
	})

	mux.handle("POST /api/v1/changesets/{changeset}/apply", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Operations []int `json:"operations"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := cs.Apply(r.Context(), r.PathValue("changeset"), in.Operations)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetJSON(v))
	})

	mux.handle("POST /api/v1/changesets/{changeset}/reject", func(w http.ResponseWriter, r *http.Request) {
		v, err := cs.Reject(r.Context(), r.PathValue("changeset"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetJSON(v))
	})
}
