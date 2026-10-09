package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

type organizationJSON struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `json:"version"`
}

func toOrganizationJSON(c tenancy.Organization) organizationJSON {
	return organizationJSON{ID: c.ID, Key: c.Key, Name: c.Name, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version}
}

type projectJSON struct {
	ID           string    `json:"id"`
	Key          string    `json:"key"`
	Organization string    `json:"organization"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Version      int64     `json:"version"`
}

func toProjectJSON(p app.ProjectView) projectJSON {
	return projectJSON{
		ID: p.ID, Key: p.Key, Organization: p.OrganizationKey, Name: p.Name, Description: p.Description,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Version: p.Version,
	}
}

type listJSON[T any] struct {
	Items []T `json:"items"`
}

func registerTenancy(mux *router, t *app.Tenancy) {
	mux.handle("POST /api/v1/organizations", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		c, err := t.CreateOrganization(r.Context(), app.CreateOrganizationInput{Key: in.Key, Name: in.Name})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toOrganizationJSON(c))
	})

	mux.handle("GET /api/v1/organizations", func(w http.ResponseWriter, r *http.Request) {
		cs, err := t.ListOrganizations(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[organizationJSON]{Items: make([]organizationJSON, 0, len(cs))}
		for _, c := range cs {
			out.Items = append(out.Items, toOrganizationJSON(c))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("GET /api/v1/organizations/{organization}", func(w http.ResponseWriter, r *http.Request) {
		c, err := t.GetOrganization(r.Context(), r.PathValue("organization"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toOrganizationJSON(c))
	})

	mux.handle("PATCH /api/v1/organizations/{organization}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    string `json:"name"`
			Version int64  `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		c, err := t.UpdateOrganization(r.Context(), app.UpdateOrganizationInput{
			Key: r.PathValue("organization"), Name: in.Name, Version: in.Version,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toOrganizationJSON(c))
	})

	mux.handle("POST /api/v1/organizations/{organization}/projects", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key         string `json:"key"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		organization := r.PathValue("organization")
		p, err := t.CreateProject(r.Context(), app.CreateProjectInput{
			OrganizationKey: organization, Key: in.Key, Name: in.Name, Description: in.Description,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toProjectJSON(app.ProjectView{Project: p, OrganizationKey: organization}))
	})

	mux.handle("GET /api/v1/organizations/{organization}/projects", func(w http.ResponseWriter, r *http.Request) {
		ps, err := t.ListProjects(r.Context(), r.PathValue("organization"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[projectJSON]{Items: make([]projectJSON, 0, len(ps))}
		for _, p := range ps {
			out.Items = append(out.Items, toProjectJSON(p))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("GET /api/v1/projects/{project}", func(w http.ResponseWriter, r *http.Request) {
		p, err := t.GetProject(r.Context(), r.PathValue("project"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toProjectJSON(p))
	})

	mux.handle("PATCH /api/v1/projects/{project}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Version     int64  `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		p, err := t.UpdateProject(r.Context(), app.UpdateProjectInput{
			Key: r.PathValue("project"), Name: in.Name, Description: in.Description, Version: in.Version,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toProjectJSON(p))
	})
}
