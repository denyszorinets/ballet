// Package proxy is the LLM gateway's request path (ADR-0011): it
// authenticates run tokens, resolves the provider credential of the run's
// customer/project through Core, and forwards the request — streaming
// included — with the real key, which callers never see.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/denyszorinets/ballet/gateway/internal/core"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Audience of run tokens accepted by the gateway.
const Audience = "gateway"

// CredentialResolver resolves provider credentials (Core).
type CredentialResolver interface {
	ResolveCredential(ctx context.Context, customer, project, provider string) (core.Credential, error)
}

// Anthropic proxies the Anthropic API (/v1/...).
type Anthropic struct {
	Verifier *runtoken.Verifier
	Core     CredentialResolver
	// DefaultURL is used when the credential has no base URL.
	DefaultURL string
	Logger     *slog.Logger
	// Observe, if set, wraps successful responses (usage metering).
	Observe func(claims runtoken.Claims, resp *http.Response) error
}

type ctxKey struct{}

type target struct {
	url    *url.URL
	key    string
	claims runtoken.Claims
}

// ServeHTTP authenticates and forwards one request.
func (a *Anthropic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := bearer(r)
	if raw == "" {
		apiError(w, http.StatusUnauthorized, "authentication_error", "a Ballet run token is required")
		return
	}
	claims, err := a.Verifier.Verify(r.Context(), raw, Audience)
	if err != nil {
		apiError(w, http.StatusUnauthorized, "authentication_error", "invalid run token")
		return
	}
	if !claims.Can(runtoken.CapLLMInvoke) {
		apiError(w, http.StatusForbidden, "permission_error", "run token lacks llm.invoke")
		return
	}
	cred, err := a.Core.ResolveCredential(r.Context(), claims.Customer, claims.Project, "anthropic")
	if errors.Is(err, core.ErrNoCredential) {
		apiError(w, http.StatusForbidden, "permission_error", "no Anthropic credential configured for this project")
		return
	}
	if err != nil {
		a.logger().ErrorContext(r.Context(), "resolve credential failed", "error", err)
		apiError(w, http.StatusBadGateway, "api_error", "credential lookup failed")
		return
	}
	base := cred.BaseURL
	if base == "" {
		base = a.DefaultURL
	}
	u, err := url.Parse(base)
	if err != nil {
		apiError(w, http.StatusBadGateway, "api_error", "invalid provider URL")
		return
	}
	ctx := context.WithValue(r.Context(), ctxKey{}, target{url: u, key: cred.APIKey, claims: claims})
	a.reverseProxy().ServeHTTP(w, r.WithContext(ctx))
}

func (a *Anthropic) reverseProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(ctxKey{}).(target)
			pr.SetURL(t.url)
			pr.Out.Host = t.url.Host
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Set("x-api-key", t.key)
		},
		FlushInterval: -1, // stream server-sent events as they arrive
		ModifyResponse: func(resp *http.Response) error {
			if a.Observe == nil {
				return nil
			}
			t := resp.Request.Context().Value(ctxKey{}).(target)
			return a.Observe(t.claims, resp)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			a.logger().WarnContext(r.Context(), "provider request failed", "error", err)
			apiError(w, http.StatusBadGateway, "api_error", "provider unreachable")
		},
	}
}

func (a *Anthropic) logger() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

// bearer returns the run token from Authorization: Bearer or x-api-key
// (agents configured with an API key send it there).
func bearer(r *http.Request) string {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return tok
	}
	return r.Header.Get("x-api-key")
}

// apiError writes an error in the Anthropic API format.
func apiError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}})
}
