// Package fakeprovider emulates the Anthropic Messages API for tests:
// non-streaming and streaming (SSE) responses with usage, and records the
// requests it received.
package fakeprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	srv := httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(srv.Close)
	p.URL = srv.URL
	return p
}

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
