package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
)

type executionJSON struct {
	Project        string            `json:"project"`
	RepoURL        string            `json:"repo_url"`
	DefaultBranch  string            `json:"default_branch"`
	Setup          []string          `json:"setup"`
	Env            map[string]string `json:"env"`
	BranchTemplate string            `json:"branch_template"`
	GitName        string            `json:"git_name"`
	GitEmail       string            `json:"git_email"`
	Forge          string            `json:"forge"`
	ForgeAPIURL    string            `json:"forge_api_url"`
	LinkTemplate   string            `json:"link_template"`
	AnswerWindow   int               `json:"answer_window_minutes"`
	Pool           string            `json:"pool"`
	FeaturePolicy  string            `json:"feature_policy"`
	UpdatedAt      *time.Time        `json:"updated_at,omitempty"`
	Version        int64             `json:"version"`
}

func toExecutionJSON(v app.ExecutionView) executionJSON {
	j := executionJSON{Project: v.ProjectKey, RepoURL: v.RepoURL, DefaultBranch: v.DefaultBranch,
		Setup: v.Setup, Env: v.Env, BranchTemplate: v.BranchTemplate, GitName: v.GitName, GitEmail: v.GitEmail,
		Forge: v.Forge, ForgeAPIURL: v.ForgeAPIURL, LinkTemplate: v.LinkTemplate, AnswerWindow: v.AnswerWindowMinutes,
		Pool: v.Pool, FeaturePolicy: v.FeaturePolicy, UpdatedAt: timePtr(v.UpdatedAt), Version: v.Version}
	if j.Setup == nil {
		j.Setup = []string{}
	}
	if j.Env == nil {
		j.Env = map[string]string{}
	}
	return j
}

func registerExecution(mux *router, ex *app.Execution) {
	mux.handle("GET /api/v1/projects/{project}/execution", func(w http.ResponseWriter, r *http.Request) {
		v, err := ex.Get(r.Context(), r.PathValue("project"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toExecutionJSON(v))
	})

	mux.handle("PUT /api/v1/projects/{project}/execution", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RepoURL        string            `json:"repo_url"`
			DefaultBranch  string            `json:"default_branch"`
			Setup          []string          `json:"setup"`
			Env            map[string]string `json:"env"`
			BranchTemplate string            `json:"branch_template"`
			GitName        string            `json:"git_name"`
			GitEmail       string            `json:"git_email"`
			Forge          string            `json:"forge"`
			ForgeAPIURL    string            `json:"forge_api_url"`
			LinkTemplate   string            `json:"link_template"`
			AnswerWindow   int               `json:"answer_window_minutes"`
			Pool           string            `json:"pool"`
			FeaturePolicy  string            `json:"feature_policy"`
			Version        int64             `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := ex.Set(r.Context(), r.PathValue("project"), execution.Settings{
			RepoURL: in.RepoURL, DefaultBranch: in.DefaultBranch, Setup: in.Setup, Env: in.Env,
			BranchTemplate: in.BranchTemplate, GitName: in.GitName, GitEmail: in.GitEmail,
			Forge: in.Forge, ForgeAPIURL: in.ForgeAPIURL, LinkTemplate: in.LinkTemplate,
			AnswerWindowMinutes: in.AnswerWindow, Pool: in.Pool, FeaturePolicy: in.FeaturePolicy,
		}, in.Version)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toExecutionJSON(v))
	})
}
