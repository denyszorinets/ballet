// Package proxy is the LLM gateway's request path (ADR-0011): it
// authenticates run tokens, resolves the provider credential of the run's
// customer/project through Core, and forwards the request — streaming
// included — with the real key, which callers never see.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/denyszorinets/ballet/gateway/internal/core"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Audience of run tokens accepted by the gateway.
const Audience = "gateway"

// maxRequestBytes bounds a request forwarded to the provider (the
// provider's own limit is of the same order).
const maxRequestBytes = 32 << 20

// upstreamTimeout bounds one provider request, including streaming.
const upstreamTimeout = 10 * time.Minute

// CredentialResolver resolves provider credentials (Core).
type CredentialResolver interface {
	ResolveCredential(ctx context.Context, customer, project, provider string) (core.Credential, error)
}

// BudgetChecker says whether work may still call the LLM (Core's budgets).
type BudgetChecker interface {
	CheckBudget(ctx context.Context, customer, project, ticket string) (allowed bool, reason string, err error)
}

// Anthropic proxies the Anthropic API (/v1/...).
type Anthropic struct {
	Verifier *runtoken.Verifier
	Core     CredentialResolver
	// Budget refuses calls of work over budget; nil: no budgets.
	Budget BudgetChecker
	// DefaultURL is used when the credential has no base URL.
	DefaultURL string
	Logger     *slog.Logger
	// Observe, if set, wraps successful responses (usage metering).
	Observe func(claims runtoken.Claims, resp *http.Response) error
	// Transport for provider requests (default http.DefaultTransport).
	Transport http.RoundTripper
}

type ctxKey struct{}

type target struct {
	url     *url.URL
	key     string
	claims  runtoken.Claims
	started *atomic.Bool // set when the provider's response headers arrive
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
	if a.Budget != nil {
		allowed, reason, err := a.Budget.CheckBudget(r.Context(), claims.Customer, claims.Project, claims.Ticket)
		switch {
		case err != nil:
			// Core also holds work over budget before each stage: fail open.
			a.logger().WarnContext(r.Context(), "budget check failed; allowing the call", "error", err)
		case !allowed:
			apiError(w, http.StatusForbidden, "permission_error", "Ballet budget exhausted: "+reason)
			return
		}
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
	// Read the whole request before forwarding it. Streamed through, the
	// provider may answer before the body is forwarded; the HTTP server then
	// discards the unread request body once the response starts, the
	// transport's write fails and it closes the upstream connection,
	// cutting the response stream (#88).
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request_error", "could not read the request")
		return
	}
	if len(body) > maxRequestBytes {
		apiError(w, http.StatusRequestEntityTooLarge, "request_too_large", "the request exceeds 32 MiB")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }

	base := cred.BaseURL
	if base == "" {
		base = a.DefaultURL
	}
	u, err := url.Parse(base)
	if err != nil {
		apiError(w, http.StatusBadGateway, "api_error", "invalid provider URL")
		return
	}
	// The upstream request is detached from the incoming request's
	// context and bounded by upstreamTimeout instead; a client that
	// disconnects is still detected when writing to it fails, which ends
	// this handler and cancels upstream.
	upstream, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), upstreamTimeout)
	defer cancel()
	var started atomic.Bool
	go func() {
		select {
		case <-r.Context().Done():
			if !started.Load() {
				a.logger().WarnContext(r.Context(), "client context ended before the provider responded; upstream continues",
					"error", r.Context().Err(), "cause", context.Cause(r.Context()))
			}
		case <-upstream.Done():
		}
	}()
	ctx := context.WithValue(upstream, ctxKey{}, target{url: u, key: cred.APIKey, claims: claims, started: &started})
	a.reverseProxy().ServeHTTP(w, r.WithContext(ctx))
}

func (a *Anthropic) reverseProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: a.Transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(ctxKey{}).(target)
			pr.SetURL(t.url)
			pr.Out.Host = t.url.Host
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Set("x-api-key", t.key)
		},
		FlushInterval: -1, // stream server-sent events as they arrive
		ModifyResponse: func(resp *http.Response) error {
			t := resp.Request.Context().Value(ctxKey{}).(target)
			t.started.Store(true)
			if a.Observe == nil {
				return nil
			}
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
