package httpapi

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// KnowledgeProxy forwards authorized knowledge requests to the Knowledge
// service with a request-scoped token (ADR-0022).
type KnowledgeProxy struct {
	URL    *url.URL // Knowledge base URL
	Access *app.KnowledgeAccess
	Tokens *runtoken.TokenIssuer
	// Transport for forwarded requests (default http.DefaultTransport).
	Transport http.RoundTripper
}

// knowledgeTokenTTL bounds forwarded tokens to the request at hand.
const knowledgeTokenTTL = 5 * time.Minute

type knowledgeTokenKey struct{}

func registerKnowledge(mux *router, kp *KnowledgeProxy) {
	if kp == nil {
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: kp.Transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(kp.URL)
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/api")
			pr.Out.URL.RawPath = ""
			pr.Out.Header.Set("Authorization", "Bearer "+pr.In.Context().Value(knowledgeTokenKey{}).(string))
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeJSON(w, http.StatusBadGateway, errorBody{Error: "internal", Message: "knowledge service unavailable"})
		},
	}
	forward := func(w http.ResponseWriter, r *http.Request) {
		write := r.Method != http.MethodGet && r.Method != http.MethodHead
		g, err := kp.Access.Authorize(r.Context(), r.PathValue("organization"), write)
		if err != nil {
			writeError(w, err)
			return
		}
		tok, err := kp.Tokens.Issue(runtoken.Claims{
			Kind: runtoken.KindService, Subject: "service:core", Audience: []string{"knowledge"},
			Organization: g.Organization, ActingFor: g.ActingFor, Capabilities: g.Capabilities,
		}, knowledgeTokenTTL)
		if err != nil {
			writeError(w, err)
			return
		}
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), knowledgeTokenKey{}, tok)))
	}
	const base = "/api/v1/organizations/{organization}/knowledge/entries"
	mux.handle("GET "+base, forward)
	mux.handle("POST "+base, forward)
	mux.handle("GET "+base+"/{entry}", forward)
	mux.handle("PATCH "+base+"/{entry}", forward)
	mux.handle("GET "+base+"/{entry}/versions", forward)
	mux.handle("GET /api/v1/organizations/{organization}/knowledge/search", forward)
}
