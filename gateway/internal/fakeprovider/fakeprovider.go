// Package fakeprovider emulates the Anthropic Messages API for tests:
// non-streaming and streaming (SSE) responses with usage, and records the
// requests it received. Streaming requests that offer tools can script a
// tool call: a last user text "/tool <name> <json input>" makes the fake
// call that tool; a following tool result is answered with
// "Tool result: <content>".
package fakeprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Usage reported by every response.
const (
	InputTokens     = 12
	OutputTokens    = 5
	CacheReadTokens = 3
)

// Request is a recorded request.
type Request struct {
	Path    string
	APIKey  string
	Headers http.Header
	Body    map[string]any
}

// Provider is a running fake.
type Provider struct {
	URL string
	Key string // the only accepted x-api-key

	mu       sync.Mutex
	requests []Request
}

// New starts a fake provider accepting key; it stops with the test.
func New(t testing.TB, key string) *Provider {
	t.Helper()
	p := &Provider{Key: key}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	p.URL = srv.URL
	return p
}

// ServeHTTP serves the fake Messages API (for standalone use).
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) { p.serve(w, r) }

// Requests returns the recorded requests.
func (p *Provider) Requests() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Request(nil), p.requests...)
}

func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	p.mu.Lock()
	p.requests = append(p.requests, Request{Path: r.URL.Path, APIKey: r.Header.Get("x-api-key"), Headers: r.Header.Clone(), Body: body})
	p.mu.Unlock()

	if r.Header.Get("x-api-key") != p.Key {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
		return
	}
	model, _ := body["model"].(string)
	if stream, _ := body["stream"].(bool); stream {
		switch step := script(body); {
		case step.result != nil:
			p.streamText(w, model, "Tool result: "+*step.result)
			return
		case step.tool != "":
			p.streamToolUse(w, model, step.tool, step.input)
			return
		}
		p.stream(w, model)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": model,
		"content": []map[string]any{{"type": "text", "text": "hello"}},
		"usage":   map[string]any{"input_tokens": InputTokens, "output_tokens": OutputTokens, "cache_read_input_tokens": CacheReadTokens},
	})
}

// stream sends SSE events with pauses so tests can observe incremental delivery.
func (p *Provider) stream(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	send := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_1", "model": model,
		"usage": map[string]any{"input_tokens": InputTokens, "output_tokens": 1, "cache_read_input_tokens": CacheReadTokens},
	}})
	for _, chunk := range []string{"hel", "lo"} {
		time.Sleep(100 * time.Millisecond)
		send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": chunk}})
	}
	send("message_delta", map[string]any{"type": "message_delta", "usage": map[string]any{"output_tokens": OutputTokens}})
	send("message_stop", map[string]any{"type": "message_stop"})
}

// blocks normalises message content to blocks.
func blocks(content any) []map[string]any {
	switch c := content.(type) {
	case string:
		return []map[string]any{{"type": "text", "text": c}}
	case []any:
		var out []map[string]any
		for _, b := range c {
			if m, ok := b.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// step is what a scripted conversation does next.
type step struct {
	tool, input string  // call this tool
	result      *string // answer with this tool result
}

// script follows a scripted conversation: the last "/tool <name> <json>"
// line in the user messages (agents add their own context around and after
// the human's text) calls that tool if tools are offered; once a tool
// result follows that line, the fake answers with it.
func script(body map[string]any) step {
	if tools, _ := body["tools"].([]any); len(tools) == 0 {
		return step{}
	}
	var next step
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		for _, b := range blocks(msg["content"]) {
			if b["type"] == "tool_result" {
				content, _ := b["content"].(string)
				if list, ok := b["content"].([]any); ok && len(list) > 0 {
					if first, ok := list[0].(map[string]any); ok {
						content, _ = first["text"].(string)
					}
				}
				if len(content) > 200 {
					content = content[:200]
				}
				next.result = &content
				continue
			}
			text, _ := b["text"].(string)
			for _, line := range strings.Split(text, "\n") {
				rest, ok := strings.CutPrefix(strings.TrimSpace(line), "/tool ")
				if !ok {
					continue
				}
				name, input, _ := strings.Cut(strings.TrimSpace(rest), " ")
				if input = strings.TrimSpace(input); input == "" {
					input = "{}"
				}
				if json.Valid([]byte(input)) {
					next = step{tool: name, input: input}
				}
			}
		}
	}
	return next
}

func (p *Provider) sse(w http.ResponseWriter) func(event string, data any) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	return func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func messageStart(send func(string, any), model string) {
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_1", "model": model,
		"usage": map[string]any{"input_tokens": InputTokens, "output_tokens": 1, "cache_read_input_tokens": CacheReadTokens},
	}})
}

func messageEnd(send func(string, any), stopReason string) {
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason},
		"usage": map[string]any{"output_tokens": OutputTokens}})
	send("message_stop", map[string]any{"type": "message_stop"})
}

// streamText streams one text block.
func (p *Provider) streamText(w http.ResponseWriter, model, text string) {
	send := p.sse(w)
	messageStart(send, model)
	send("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""}})
	send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": text}})
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	messageEnd(send, "end_turn")
}

// streamToolUse streams a short text and a call of tool name with input.
func (p *Provider) streamToolUse(w http.ResponseWriter, model, name, input string) {
	send := p.sse(w)
	messageStart(send, model)
	send("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""}})
	send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": "Calling " + name + "."}})
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	send("content_block_start", map[string]any{"type": "content_block_start", "index": 1,
		"content_block": map[string]any{"type": "tool_use", "id": "toolu_fake", "name": name, "input": map[string]any{}}})
	send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": input}})
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1})
	messageEnd(send, "tool_use")
}
