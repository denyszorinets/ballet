// Package anthropic calls the Anthropic Messages API through Ballet's LLM
// gateway (ADR-0011) with streaming and tool use, for the planner.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Version is the Anthropic API version sent with every request.
const Version = "2023-06-01"

// Client streams Messages API responses. Each call carries a short-lived
// planner run token so the gateway can resolve the project's credential
// and meter usage to the project.
type Client struct {
	GatewayURL string // e.g. http://localhost:8082
	HTTP       *http.Client
	Tokens     *runtoken.TokenIssuer
	TokenTTL   time.Duration // default 5 minutes
}

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireTool    `json:"tools,omitempty"`
	Stream    bool          `json:"stream"`
}

func toWire(req app.LLMRequest) wireRequest {
	w := wireRequest{Model: req.Model, MaxTokens: req.MaxTokens, System: req.System, Stream: true}
	for _, m := range req.Messages {
		wm := wireMessage{Role: string(m.Role)}
		for _, b := range m.Content {
			switch b.Type {
			case planner.BlockText:
				wm.Content = append(wm.Content, wireBlock{Type: "text", Text: b.Text})
			case planner.BlockToolUse:
				input := b.Input
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				wm.Content = append(wm.Content, wireBlock{Type: "tool_use", ID: b.ToolUseID, Name: b.Name, Input: input})
			case planner.BlockToolResult:
				wm.Content = append(wm.Content, wireBlock{Type: "tool_result", ToolUseID: b.ToolUseID, Content: b.Text, IsError: b.IsError})
			}
		}
		w.Messages = append(w.Messages, wm)
	}
	for _, t := range req.Tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		w.Tools = append(w.Tools, wireTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return w
}

// Stream sends req and returns the complete response, calling onText with
// each piece of streamed text.
func (c *Client) Stream(ctx context.Context, req app.LLMRequest, onText func(string)) (app.LLMResponse, error) {
	ttl := c.TokenTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	token, err := c.Tokens.Issue(runtoken.Claims{
		Kind: runtoken.KindPlanner, Subject: "planner:" + req.Caller.SessionID, Audience: []string{"gateway"},
		Customer: req.Caller.CustomerKey, Project: req.Caller.ProjectKey, Session: req.Caller.SessionID,
		ActingFor: req.Caller.ActingFor, Capabilities: []string{runtoken.CapLLMInvoke},
	}, ttl)
	if err != nil {
		return app.LLMResponse{}, err
	}
	body, err := json.Marshal(toWire(req))
	if err != nil {
		return app.LLMResponse{}, fmt.Errorf("anthropic: encode request: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.GatewayURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return app.LLMResponse{}, err
	}
	hreq.Header.Set("Authorization", "Bearer "+token)
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("anthropic-version", Version)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		return app.LLMResponse{}, fmt.Errorf("anthropic: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return app.LLMResponse{}, fmt.Errorf("anthropic: %s: %s", resp.Status, apiMessage(data))
	}
	return parseStream(resp.Body, onText)
}

// apiMessage extracts the message of an API error body.
func apiMessage(data []byte) string {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		return e.Error.Type + ": " + e.Error.Message
	}
	return strings.TrimSpace(string(data))
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage wireUsage `json:"usage"`
	} `json:"message"`
	ContentBlock wireBlock `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage wireUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type wireUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// parseStream reads server-sent events into a response.
func parseStream(r io.Reader, onText func(string)) (app.LLMResponse, error) {
	var out app.LLMResponse
	blocks := map[int]*planner.Block{}
	inputs := map[int]*strings.Builder{}
	var order []int
	block := func(i int) *planner.Block {
		if b, ok := blocks[i]; ok {
			return b
		}
		b := &planner.Block{Type: planner.BlockText}
		blocks[i], order = b, append(order, i)
		return b
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	stopped := false
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &ev); err != nil {
			return app.LLMResponse{}, fmt.Errorf("anthropic: bad stream event: %w", err)
		}
		switch ev.Type {
		case "message_start":
			u := ev.Message.Usage
			out.Usage = planner.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
				CacheReadTokens: u.CacheReadInputTokens, CacheWriteTokens: u.CacheCreationInputTokens}
		case "content_block_start":
			b := block(ev.Index)
			if ev.ContentBlock.Type == "tool_use" {
				b.Type, b.ToolUseID, b.Name = planner.BlockToolUse, ev.ContentBlock.ID, ev.ContentBlock.Name
				inputs[ev.Index] = &strings.Builder{}
			}
			b.Text += ev.ContentBlock.Text
		case "content_block_delta":
			b := block(ev.Index)
			switch ev.Delta.Type {
			case "text_delta":
				b.Text += ev.Delta.Text
				if ev.Delta.Text != "" {
					onText(ev.Delta.Text)
				}
			case "input_json_delta":
				if inputs[ev.Index] == nil {
					inputs[ev.Index] = &strings.Builder{}
				}
				inputs[ev.Index].WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				out.StopReason = ev.Delta.StopReason
			}
			if ev.Usage.OutputTokens > 0 {
				out.Usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			stopped = true
		case "error":
			return app.LLMResponse{}, fmt.Errorf("anthropic: %s: %s", ev.Error.Type, ev.Error.Message)
		}
	}
	if err := sc.Err(); err != nil {
		return app.LLMResponse{}, fmt.Errorf("anthropic: read stream: %w", err)
	}
	if !stopped {
		return app.LLMResponse{}, fmt.Errorf("anthropic: stream ended before message_stop")
	}
	for _, i := range order {
		b := *blocks[i]
		if b.Type == planner.BlockToolUse {
			input := strings.TrimSpace(inputs[i].String())
			if input == "" {
				input = "{}"
			}
			if !json.Valid([]byte(input)) {
				return app.LLMResponse{}, fmt.Errorf("anthropic: tool %s input is not valid JSON", b.Name)
			}
			b.Input = json.RawMessage(input)
		}
		if b.Type == planner.BlockText && b.Text == "" {
			continue
		}
		out.Content = append(out.Content, b)
	}
	return out, nil
}
