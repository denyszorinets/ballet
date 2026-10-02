package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
)

type runJSON struct {
	ID         string     `json:"id"`
	Project    string     `json:"project"`
	Ticket     string     `json:"ticket"`
	Stage      string     `json:"stage"`
	Status     run.Status `json:"status"`
	Spec       run.Spec   `json:"spec"`
	Branch     string     `json:"branch,omitempty"`
	Runner     string     `json:"runner,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Version    int64      `json:"version"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func toRunJSON(v app.RunView) runJSON {
	spec := v.Spec
	if spec.Command == nil {
		spec.Command = []string{}
	}
	return runJSON{ID: v.ID, Project: v.ProjectKey, Ticket: v.TicketKey, Stage: v.Stage, Status: v.Status, Spec: spec,
		Branch: v.Branch, Runner: v.Runner, ExitCode: v.ExitCode, Error: v.Error, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt,
		StartedAt: timePtr(v.StartedAt), FinishedAt: timePtr(v.FinishedAt), Version: v.Version}
}

type runLogJSON struct {
	Seq    int64     `json:"seq"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

func registerRuns(mux *router, rs *app.Runs) {
	mux.handle("GET /api/v1/items/{item}/runs", func(w http.ResponseWriter, r *http.Request) {
		list, err := rs.ListForTicket(r.Context(), r.PathValue("item"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[runJSON]{Items: make([]runJSON, 0, len(list))}
		for _, v := range list {
			out.Items = append(out.Items, toRunJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("POST /api/v1/items/{item}/runs", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Stage string   `json:"stage"`
			Spec  run.Spec `json:"spec"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := rs.Create(r.Context(), r.PathValue("item"), in.Stage, in.Spec)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toRunJSON(v))
	})

	mux.handle("GET /api/v1/runs/{run}", func(w http.ResponseWriter, r *http.Request) {
		v, err := rs.Get(r.Context(), r.PathValue("run"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toRunJSON(v))
	})

	mux.handle("GET /api/v1/runs/{run}/logs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		after, err := optionalInt(q.Get("after"))
		if err != nil {
			writeError(w, err)
			return
		}
		limit, err := optionalInt(q.Get("limit"))
		if err != nil {
			writeError(w, err)
			return
		}
		logs, err := rs.Logs(r.Context(), r.PathValue("run"), int64(after), limit)
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[runLogJSON]{Items: make([]runLogJSON, 0, len(logs))}
		for _, l := range logs {
			out.Items = append(out.Items, runLogJSON{Seq: l.Seq, Stream: l.Stream, Text: l.Text, At: l.At})
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("POST /api/v1/runs/{run}/cancel", func(w http.ResponseWriter, r *http.Request) {
		v, err := rs.Cancel(r.Context(), r.PathValue("run"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toRunJSON(v))
	})
}

func optionalInt(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: expected a non-negative integer, got %q", app.ErrInvalid, s)
	}
	return n, nil
}
