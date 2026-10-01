package httpapi

import (
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
)

type bindingJSON struct {
	ID        string     `json:"id"`
	Claim     string     `json:"claim"`
	Value     string     `json:"value"`
	Role      string     `json:"role"`
	Scope     string     `json:"scope"`
	Bootstrap bool       `json:"bootstrap"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

func toBindingJSON(b rbac.Binding) bindingJSON {
	out := bindingJSON{
		ID: b.ID, Claim: b.Claim, Value: b.Value, Role: string(b.Role), Scope: b.Scope.String(), Bootstrap: b.Bootstrap,
	}
	if !b.CreatedAt.IsZero() {
		out.CreatedAt = &b.CreatedAt
	}
	return out
}

type roleJSON struct {
	Role    string   `json:"role"`
	Actions []string `json:"actions"`
}

func registerRBAC(mux *http.ServeMux, rb *app.RoleBindings) {
	mux.HandleFunc("GET /api/v1/roles", func(w http.ResponseWriter, _ *http.Request) {
		out := listJSON[roleJSON]{}
		for _, r := range rbac.AllRoles {
			actions := []string{}
			for _, a := range r.Actions() {
				actions = append(actions, string(a))
			}
			out.Items = append(out.Items, roleJSON{Role: string(r), Actions: actions})
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("GET /api/v1/role-bindings", func(w http.ResponseWriter, r *http.Request) {
		bs, err := rb.List(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[bindingJSON]{Items: make([]bindingJSON, 0, len(bs))}
		for _, b := range bs {
			out.Items = append(out.Items, toBindingJSON(b))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("POST /api/v1/role-bindings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Claim string `json:"claim"`
			Value string `json:"value"`
			Role  string `json:"role"`
			Scope string `json:"scope"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		b, err := rb.Create(r.Context(), app.CreateRoleBindingInput{Claim: in.Claim, Value: in.Value, Role: in.Role, Scope: in.Scope})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toBindingJSON(b))
	})

	mux.HandleFunc("DELETE /api/v1/role-bindings/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := rb.Delete(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
