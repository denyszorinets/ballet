package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/credential"
)

type credentialJSON struct {
	Provider    credential.Provider `json:"provider"`
	Project     string              `json:"project,omitempty"`
	BaseURL     string              `json:"base_url"`
	Fingerprint string              `json:"fingerprint"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

func toCredentialJSON(v app.CredentialView) credentialJSON {
	return credentialJSON{Provider: v.Provider, Project: v.ProjectKey, BaseURL: v.BaseURL, Fingerprint: v.Fingerprint, UpdatedAt: v.UpdatedAt}
}

type setCredentialBody struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
}

func registerCredentials(mux *router, cr *app.Credentials) {
	mux.handle("GET /api/v1/organizations/{organization}/credentials", func(w http.ResponseWriter, r *http.Request) {
		list, err := cr.List(r.Context(), r.PathValue("organization"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[credentialJSON]{Items: make([]credentialJSON, 0, len(list))}
		for _, v := range list {
			out.Items = append(out.Items, toCredentialJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})

	set := func(organizationKey, projectKey string, w http.ResponseWriter, r *http.Request) {
		var in setCredentialBody
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := cr.Set(r.Context(), app.SetCredentialInput{
			OrganizationKey: organizationKey, ProjectKey: projectKey,
			Provider: credential.Provider(r.PathValue("provider")), APIKey: in.APIKey, BaseURL: in.BaseURL,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toCredentialJSON(v))
	}
	del := func(organizationKey, projectKey string, w http.ResponseWriter, r *http.Request) {
		if err := cr.Delete(r.Context(), organizationKey, projectKey, credential.Provider(r.PathValue("provider"))); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
	mux.handle("PUT /api/v1/organizations/{organization}/credentials/{provider}", func(w http.ResponseWriter, r *http.Request) {
		set(r.PathValue("organization"), "", w, r)
	})
	mux.handle("DELETE /api/v1/organizations/{organization}/credentials/{provider}", func(w http.ResponseWriter, r *http.Request) {
		del(r.PathValue("organization"), "", w, r)
	})
	mux.handle("PUT /api/v1/projects/{project}/credentials/{provider}", func(w http.ResponseWriter, r *http.Request) {
		set("", r.PathValue("project"), w, r)
	})
	mux.handle("DELETE /api/v1/projects/{project}/credentials/{provider}", func(w http.ResponseWriter, r *http.Request) {
		del("", r.PathValue("project"), w, r)
	})
}
