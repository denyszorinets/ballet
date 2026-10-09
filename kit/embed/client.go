package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Gateway embeds through the LLM gateway's /v1/embeddings endpoint with a
// service token, attributing usage to an organization (and project).
type Gateway struct {
	URL          string // gateway base URL
	Token        func(context.Context) (string, error)
	ModelID      string
	Organization string
	Project      string
	HTTP         *http.Client
}

// Model returns the configured model.
func (g *Gateway) Model() string { return g.ModelID }

// Embed calls the gateway.
func (g *Gateway) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": g.ModelID, "input": texts})
	if err != nil {
		return nil, err
	}
	tok, err := g.Token(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderOrganization, g.Organization)
	if g.Project != "" {
		req.Header.Set(HeaderProject, g.Project)
	}
	client := g.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: gateway returned %s", resp.Status)
	}
	var out Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	for i, v := range vecs {
		if v == nil {
			return nil, fmt.Errorf("embed: no embedding for input %d", i)
		}
	}
	return vecs, nil
}

// Headers by which service callers name the organization/project an
// embedding request is made for (ignored for run tokens).
const (
	HeaderOrganization = "X-Ballet-Organization"
	HeaderProject      = "X-Ballet-Project"
)

// Response is the OpenAI-compatible embeddings response.
type Response struct {
	Object string  `json:"object"`
	Model  string  `json:"model"`
	Data   []Datum `json:"data"`
	Usage  struct {
		PromptTokens int64 `json:"prompt_tokens"`
		TotalTokens  int64 `json:"total_tokens"`
	} `json:"usage"`
}

// Datum is one embedding.
type Datum struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}
