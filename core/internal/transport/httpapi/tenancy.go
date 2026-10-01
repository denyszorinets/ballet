package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

type customerJSON struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `json:"version"`
}

func toCustomerJSON(c tenancy.Customer) customerJSON {
	return customerJSON{ID: c.ID, Key: c.Key, Name: c.Name, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version}
}

type projectJSON struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	Customer    string    `json:"customer"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int64     `json:"version"`
}

func toProjectJSON(p app.ProjectView) projectJSON {
	return projectJSON{
		ID: p.ID, Key: p.Key, Customer: p.CustomerKey, Name: p.Name, Description: p.Description,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Version: p.Version,
	}
}

type listJSON[T any] struct {
	Items []T `json:"items"`
}

func registerTenancy(mux *router, t *app.Tenancy) {
	mux.handle("POST /api/v1/customers", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		c, err := t.CreateCustomer(r.Context(), app.CreateCustomerInput{Key: in.Key, Name: in.Name})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toCustomerJSON(c))
	})

	mux.handle("GET /api/v1/customers", func(w http.ResponseWriter, r *http.Request) {
		cs, err := t.ListCustomers(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[customerJSON]{Items: make([]customerJSON, 0, len(cs))}
		for _, c := range cs {
			out.Items = append(out.Items, toCustomerJSON(c))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("GET /api/v1/customers/{customer}", func(w http.ResponseWriter, r *http.Request) {
		c, err := t.GetCustomer(r.Context(), r.PathValue("customer"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toCustomerJSON(c))
	})

	mux.handle("PATCH /api/v1/customers/{customer}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    string `json:"name"`
			Version int64  `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		c, err := t.UpdateCustomer(r.Context(), app.UpdateCustomerInput{
			Key: r.PathValue("customer"), Name: in.Name, Version: in.Version,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toCustomerJSON(c))
	})

	mux.handle("POST /api/v1/customers/{customer}/projects", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key         string `json:"key"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		customer := r.PathValue("customer")
		p, err := t.CreateProject(r.Context(), app.CreateProjectInput{
			CustomerKey: customer, Key: in.Key, Name: in.Name, Description: in.Description,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toProjectJSON(app.ProjectView{Project: p, CustomerKey: customer}))
	})

	mux.handle("GET /api/v1/customers/{customer}/projects", func(w http.ResponseWriter, r *http.Request) {
		ps, err := t.ListProjects(r.Context(), r.PathValue("customer"))
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
