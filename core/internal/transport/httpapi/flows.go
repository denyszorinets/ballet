package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
)

type flowStageJSON struct {
	ID   string        `json:"id"`
	Kind pipeline.Kind `json:"kind"`
	Name string        `json:"name,omitempty"`
}

type flowJSON struct {
	Ticket          string          `json:"ticket"`
	Pipeline        string          `json:"pipeline"`
	PipelineVersion int64           `json:"pipeline_version"`
	Stages          []flowStageJSON `json:"stages"`
	Stage           string          `json:"stage"`
	Iteration       int             `json:"iteration"`
	MaxIterations   int             `json:"max_iterations"`
	Status          app.FlowStatus  `json:"status"`
	Waiting         string          `json:"waiting,omitempty"`
	Run             string          `json:"run,omitempty"`
	Outcome         string          `json:"outcome,omitempty"`
	Report          string          `json:"report,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	Version         int64           `json:"version"`
}

func toFlowJSON(v app.FlowView) flowJSON {
	j := flowJSON{Ticket: v.TicketKey, Pipeline: v.Pipeline, PipelineVersion: v.PipelineVersion, Stage: v.Stage,
		Iteration: v.Iteration, MaxIterations: v.Definition.MaxIterations, Status: v.Status, Waiting: v.Waiting,
		Run: v.RunID, Outcome: v.Outcome, Report: v.Report, StartedAt: v.StartedAt, UpdatedAt: v.UpdatedAt,
		Version: v.Version, Stages: []flowStageJSON{}}
	for _, s := range v.Definition.Stages {
		j.Stages = append(j.Stages, flowStageJSON{ID: s.ID, Kind: s.Kind, Name: s.Name})
	}
	return j
}

func registerFlows(mux *router, fl *app.Flows) {
	serve := func(f func(r *http.Request) (app.FlowView, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			v, err := f(r)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, toFlowJSON(v))
		}
	}
	decide := func(approve bool) http.HandlerFunc {
		return serve(func(r *http.Request) (app.FlowView, error) {
			var in struct {
				Comment string `json:"comment"`
			}
			if err := decode(r, &in); err != nil {
				return app.FlowView{}, err
			}
			return fl.Decide(r.Context(), r.PathValue("item"), approve, in.Comment)
		})
	}
	mux.handle("GET /api/v1/items/{item}/flow", serve(func(r *http.Request) (app.FlowView, error) {
		return fl.Get(r.Context(), r.PathValue("item"))
	}))
	mux.handle("POST /api/v1/items/{item}/flow/start", serve(func(r *http.Request) (app.FlowView, error) {
		return fl.Start(r.Context(), r.PathValue("item"))
	}))
	mux.handle("POST /api/v1/items/{item}/flow/approve", decide(true))
	mux.handle("POST /api/v1/items/{item}/flow/reject", decide(false))
}
