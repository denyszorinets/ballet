package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
)

type plannerSessionJSON struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Title     string    `json:"title"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Running   bool      `json:"running"`
}

type plannerMessageJSON struct {
	Seq        int64           `json:"seq"`
	Role       planner.Role    `json:"role"`
	Content    []planner.Block `json:"content"`
	Author     string          `json:"author,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	Usage      planner.Usage   `json:"usage"`
	CreatedAt  time.Time       `json:"created_at"`
}

type plannerTranscriptJSON struct {
	plannerSessionJSON
	Messages []plannerMessageJSON `json:"messages"`
}

func toSessionJSON(v app.SessionView) plannerSessionJSON {
	return plannerSessionJSON{ID: v.ID, Project: v.ProjectKey, Title: v.Title, CreatedBy: v.CreatedBy,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Running: v.Running}
}

func registerPlanner(mux *router, pl *app.Planner) {
	mux.handle("GET /api/v1/projects/{project}/planner/sessions", func(w http.ResponseWriter, r *http.Request) {
		list, err := pl.ListSessions(r.Context(), r.PathValue("project"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[plannerSessionJSON]{Items: make([]plannerSessionJSON, 0, len(list))}
		for _, v := range list {
			out.Items = append(out.Items, toSessionJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("POST /api/v1/projects/{project}/planner/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := pl.CreateSession(r.Context(), r.PathValue("project"), in.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toSessionJSON(v))
	})

	mux.handle("GET /api/v1/planner/sessions/{session}", func(w http.ResponseWriter, r *http.Request) {
		v, msgs, err := pl.Session(r.Context(), r.PathValue("session"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := plannerTranscriptJSON{plannerSessionJSON: toSessionJSON(v), Messages: make([]plannerMessageJSON, 0, len(msgs))}
		for _, m := range msgs {
			content := m.Content
			if content == nil {
				content = []planner.Block{}
			}
			out.Messages = append(out.Messages, plannerMessageJSON{Seq: m.Seq, Role: m.Role, Content: content,
				Author: m.Author, StopReason: m.StopReason, Usage: m.Usage, CreatedAt: m.CreatedAt})
		}
		writeJSON(w, http.StatusOK, out)
	})
}
