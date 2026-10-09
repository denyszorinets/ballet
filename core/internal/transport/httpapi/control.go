package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
)

type pauseJSON struct {
	Scope    string    `json:"scope"` // "platform" or "project"
	Project  string    `json:"project,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	PausedBy string    `json:"paused_by"`
	PausedAt time.Time `json:"paused_at"`
}

func toPauseJSON(v app.PauseView) pauseJSON {
	out := pauseJSON{Scope: "project", Project: v.ProjectKey, Reason: v.Reason, PausedBy: v.PausedBy, PausedAt: v.PausedAt}
	if v.Scope == app.PausePlatform {
		out.Scope = "platform"
	}
	return out
}

func registerControl(mux *router, ct *app.Control) {
	mux.handle("GET /api/v1/pauses", func(w http.ResponseWriter, r *http.Request) {
		list, err := ct.List(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[pauseJSON]{Items: make([]pauseJSON, 0, len(list))}
		for _, v := range list {
			out.Items = append(out.Items, toPauseJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})
	type reasonIn struct {
		Reason string `json:"reason"`
	}
	pause := func(project func(*http.Request) string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in reasonIn
			if err := decode(r, &in); err != nil {
				writeError(w, err)
				return
			}
			v, err := ct.Pause(r.Context(), project(r), in.Reason)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, toPauseJSON(v))
		}
	}
	resume := func(project func(*http.Request) string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := ct.Resume(r.Context(), project(r)); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	kill := func(project func(*http.Request) string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in reasonIn
			if err := decode(r, &in); err != nil {
				writeError(w, err)
				return
			}
			v, n, err := ct.Kill(r.Context(), project(r), in.Reason)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, struct {
				Pause     pauseJSON `json:"pause"`
				Cancelled int       `json:"cancelled"`
			}{toPauseJSON(v), n})
		}
	}
	platform := func(*http.Request) string { return "" }
	proj := func(r *http.Request) string { return r.PathValue("project") }
	mux.handle("PUT /api/v1/pause", pause(platform))
	mux.handle("DELETE /api/v1/pause", resume(platform))
	mux.handle("POST /api/v1/kill", kill(platform))
	mux.handle("PUT /api/v1/projects/{project}/pause", pause(proj))
	mux.handle("DELETE /api/v1/projects/{project}/pause", resume(proj))
	mux.handle("POST /api/v1/projects/{project}/kill", kill(proj))
}
