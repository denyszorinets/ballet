package internalapi

import (
	"errors"
	"net/http"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/credential"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// ResolvedCredential is returned to the gateway.
type ResolvedCredential struct {
	Provider credential.Provider `json:"provider"`
	APIKey   string              `json:"api_key"`
	BaseURL  string              `json:"base_url"`
}

// RegisterCredentials adds GET /internal/v1/credentials/resolve
// (?customer=<key>&project=<key>&provider=<anthropic|openai>) for holders
// of credentials.read.
func RegisterCredentials(r *Router, cr *app.Credentials) {
	r.Handle("GET /internal/v1/credentials/resolve", runtoken.CapCredentialsRead, func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		provider := credential.Provider(q.Get("provider"))
		if !provider.LLM() {
			// Git tokens are delivered with runs only; no service reads them.
			WriteError(w, http.StatusBadRequest, "invalid_argument", "provider must be an LLM provider")
			return
		}
		c, err := cr.Resolve(req.Context(), q.Get("customer"), q.Get("project"), provider)
		if errors.Is(err, app.ErrNotFound) {
			WriteError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		WriteJSON(w, http.StatusOK, ResolvedCredential{Provider: c.Provider, APIKey: c.APIKey, BaseURL: c.BaseURL})
	})
}
