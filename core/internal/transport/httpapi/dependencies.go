package httpapi

import (
	"net/http"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

type itemRefJSON struct {
	Key   string        `json:"key"`
	Kind  tracker.Kind  `json:"kind"`
	Title string        `json:"title"`
	State tracker.State `json:"state"`
}

type dependencyJSON struct {
	ID   string        `json:"id"`
	Type app.Direction `json:"type"`
	Item itemRefJSON   `json:"item"`
}

func toDependencyJSON(d app.DependencyView) dependencyJSON {
	return dependencyJSON{ID: d.ID, Type: d.Direction, Item: itemRefJSON{
		Key: d.Other.Key, Kind: d.Other.Kind, Title: d.Other.Title, State: d.Other.State,
	}}
}

func registerDependencies(mux *router, t *app.Tracker) {
	mux.handle("POST /api/v1/items/{item}/dependencies", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Type app.Direction `json:"type"`
			Item string        `json:"item"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		d, err := t.AddDependency(r.Context(), r.PathValue("item"), in.Type, in.Item)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toDependencyJSON(d))
	})

	mux.handle("GET /api/v1/items/{item}/dependencies", func(w http.ResponseWriter, r *http.Request) {
		deps, err := t.Dependencies(r.Context(), r.PathValue("item"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[dependencyJSON]{Items: make([]dependencyJSON, 0, len(deps))}
		for _, d := range deps {
			out.Items = append(out.Items, toDependencyJSON(d))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("DELETE /api/v1/dependencies/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := t.RemoveDependency(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.handle("GET /api/v1/projects/{project}/runnable", func(w http.ResponseWriter, r *http.Request) {
		items, err := t.Runnable(r.Context(), r.PathValue("project"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[itemJSON]{Items: make([]itemJSON, 0, len(items))}
		for _, v := range items {
			out.Items = append(out.Items, toItemJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})
}
