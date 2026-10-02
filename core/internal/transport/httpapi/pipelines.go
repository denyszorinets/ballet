package httpapi

import (
	"net/http"

	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/pipeline"
)

type pipelineJSON struct {
	Project    string              `json:"project"`
	Name       string              `json:"name"`
	Version    int64               `json:"version"`
	Definition pipeline.Definition `json:"definition"`
	CreatedBy  string              `json:"created_by"`
	CreatedAt  *time.Time          `json:"created_at,omitempty"`
}

func toPipelineJSON(v app.PipelineView) pipelineJSON {
	return pipelineJSON{Project: v.ProjectKey, Name: v.Name, Version: v.Version, Definition: v.Definition,
		CreatedBy: v.CreatedBy, CreatedAt: timePtr(v.CreatedAt)}
}

func registerPipelines(mux *router, ps *app.Pipelines) {
	mux.handle("GET /api/v1/projects/{project}/pipelines", func(w http.ResponseWriter, r *http.Request) {
		list, err := ps.List(r.Context(), r.PathValue("project"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[pipelineJSON]{Items: make([]pipelineJSON, 0, len(list))}
		for _, v := range list {
			out.Items = append(out.Items, toPipelineJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("GET /api/v1/projects/{project}/pipelines/{name}", func(w http.ResponseWriter, r *http.Request) {
		version, err := optionalInt(r.URL.Query().Get("version"))
		if err != nil {
			writeError(w, err)
			return
		}
		v, err := ps.Get(r.Context(), r.PathValue("project"), r.PathValue("name"), int64(version))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toPipelineJSON(v))
	})

	mux.handle("GET /api/v1/projects/{project}/pipelines/{name}/versions", func(w http.ResponseWriter, r *http.Request) {
		list, err := ps.Versions(r.Context(), r.PathValue("project"), r.PathValue("name"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[pipelineJSON]{Items: make([]pipelineJSON, 0, len(list))}
		for _, v := range list {
			out.Items = append(out.Items, toPipelineJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("PUT /api/v1/projects/{project}/pipelines/{name}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Definition pipeline.Definition `json:"definition"`
			Version    int64               `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := ps.Save(r.Context(), r.PathValue("project"), r.PathValue("name"), in.Definition, in.Version)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toPipelineJSON(v))
	})

	mux.handle("POST /api/v1/pipelines/parse", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			YAML string `json:"yaml"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		out := struct {
			Definition *pipeline.Definition `json:"definition,omitempty"`
			Errors     []string             `json:"errors"`
		}{Errors: []string{}}
		d, err := pipeline.ParseYAML(in.YAML)
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
		} else {
			out.Definition = &d
			if err := d.Validate(ps.Adapters); err != nil {
				out.Errors = append(out.Errors, splitErrors(err)...)
			}
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("POST /api/v1/pipelines/render", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Definition pipeline.Definition `json:"definition"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		src, err := in.Definition.YAML()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"yaml": src})
	})
}

// splitErrors flattens joined errors into messages.
func splitErrors(err error) []string {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []string
		for _, e := range j.Unwrap() {
			out = append(out, splitErrors(e)...)
		}
		return out
	}
	return []string{err.Error()}
}
