// Package embeddings serves the gateway's OpenAI-compatible embeddings
// endpoint: the built-in hash model locally, other models through the
// customer's "openai" credential.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/gateway/internal/core"
	"github.com/denyszorinets/ballet/gateway/internal/usage"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/embed"
)

// CredentialResolver resolves provider credentials (Core).
type CredentialResolver interface {
	ResolveCredential(ctx context.Context, customer, project, provider string) (core.Credential, error)
}

// Handler serves POST /v1/embeddings.
type Handler struct {
	Verifier     *runtoken.Verifier
	Core         CredentialResolver
	DefaultModel string // used when the request names no model
	OpenAIURL    string // used when the credential has no base URL
	Sink         usage.Sink
	HTTP         *http.Client
	Logger       *slog.Logger
	Now          func() time.Time
}

type request struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

// scope is who a request is made for.
type scope struct {
	run, customer, project, ticket string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, http.StatusMethodNotAllowed, "invalid_request_error", "POST required")
		return
	}
	sc, status, msg := h.authenticate(r)
	if status != 0 {
		apiError(w, status, "authentication_error", msg)
		return
	}
	var req request
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		return
	}
	inputs, err := parseInput(req.Input)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if req.Model == "" {
		req.Model = h.DefaultModel
	}

	var resp embed.Response
	if req.Model == embed.HashModel {
		resp = hashResponse(r.Context(), inputs)
	} else {
		var code int
		resp, code, err = h.forward(r.Context(), sc, req.Model, inputs)
		if err != nil {
			apiError(w, code, "api_error", err.Error())
			return
		}
	}
	if h.Sink != nil {
		h.Sink(usage.Record{
			OccurredAt: h.Now(), Run: sc.run, Customer: sc.customer, Project: sc.project, Ticket: sc.ticket,
			Model: resp.Model, Status: http.StatusOK, InputTokens: resp.Usage.PromptTokens,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// authenticate accepts run tokens (llm.invoke; scope from claims) and
// service tokens (llm.embed; scope from X-Ballet-Customer/Project).
func (h *Handler) authenticate(r *http.Request) (scope, int, string) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		raw = r.Header.Get("x-api-key")
	}
	if raw == "" {
		return scope{}, http.StatusUnauthorized, "a Ballet token is required"
	}
	c, err := h.Verifier.Verify(r.Context(), raw, "gateway")
	if err != nil {
		return scope{}, http.StatusUnauthorized, "invalid token"
	}
	if c.Kind == runtoken.KindService {
		if !c.Can(runtoken.CapLLMEmbed) {
			return scope{}, http.StatusForbidden, "token lacks llm.embed"
		}
		cust := r.Header.Get(embed.HeaderCustomer)
		if cust == "" {
			return scope{}, http.StatusBadRequest, embed.HeaderCustomer + " is required for service tokens"
		}
		return scope{run: c.Subject, customer: cust, project: r.Header.Get(embed.HeaderProject)}, 0, ""
	}
	if !c.Can(runtoken.CapLLMInvoke) {
		return scope{}, http.StatusForbidden, "token lacks llm.invoke"
	}
	return scope{run: c.Subject, customer: c.Customer, project: c.Project, ticket: c.Ticket}, 0, ""
}

func parseInput(raw json.RawMessage) ([]string, error) {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil || len(many) == 0 {
		return nil, errors.New("input must be a string or a non-empty array of strings")
	}
	if len(many) > 2048 {
		return nil, errors.New("at most 2048 inputs per request")
	}
	return many, nil
}

func hashResponse(ctx context.Context, inputs []string) embed.Response {
	vecs, _ := embed.Hash{}.Embed(ctx, inputs) // never fails
	resp := embed.Response{Object: "list", Model: embed.HashModel}
	var tokens int64
	for i, v := range vecs {
		resp.Data = append(resp.Data, embed.Datum{Object: "embedding", Index: i, Embedding: v})
		tokens += int64(len(embed.Words(inputs[i])))
	}
	resp.Usage.PromptTokens, resp.Usage.TotalTokens = tokens, tokens
	return resp
}

// forward calls the customer's OpenAI-compatible provider.
func (h *Handler) forward(ctx context.Context, sc scope, model string, inputs []string) (embed.Response, int, error) {
	cred, err := h.Core.ResolveCredential(ctx, sc.customer, sc.project, "openai")
	if errors.Is(err, core.ErrNoCredential) {
		return embed.Response{}, http.StatusForbidden, errors.New("no openai credential configured for embeddings")
	}
	if err != nil {
		return embed.Response{}, http.StatusBadGateway, errors.New("credential lookup failed")
	}
	base := cred.BaseURL
	if base == "" {
		base = h.OpenAIURL
	}
	body, _ := json.Marshal(map[string]any{"model": model, "input": inputs})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return embed.Response{}, http.StatusBadGateway, errors.New("invalid provider URL")
	}
	req.Header.Set("Authorization", "Bearer "+cred.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := h.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return embed.Response{}, http.StatusBadGateway, errors.New("provider unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return embed.Response{}, http.StatusBadGateway, errors.New("provider returned " + resp.Status)
	}
	var out embed.Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return embed.Response{}, http.StatusBadGateway, errors.New("invalid provider response")
	}
	if out.Model == "" {
		out.Model = model
	}
	return out, http.StatusOK, nil
}

// apiError writes an OpenAI-style error.
func apiError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": typ, "message": msg}})
}
