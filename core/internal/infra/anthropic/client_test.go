package anthropic_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/core/internal/infra/anthropic"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var typ struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(e), &typ)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ.Type, e)
		w.(http.Flusher).Flush()
	}
}

func setup(t *testing.T, handler http.HandlerFunc) (*anthropic.Client, *runtoken.KeyRing) {
	t.Helper()
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &anthropic.Client{GatewayURL: srv.URL, Tokens: runtoken.NewIssuer(ring, time.Now)}, ring
}

var request = app.LLMRequest{
	Caller: app.LLMCaller{OrganizationKey: "acme", ProjectKey: "WEB", SessionID: "s1", ActingFor: "bob"},
	Model:  "claude-test", System: "Be brief.", MaxTokens: 100,
	Messages: []planner.Message{
		{Role: planner.RoleUser, Content: []planner.Block{planner.Text("hi")}},
		{Role: planner.RoleAssistant, Content: []planner.Block{{Type: planner.BlockToolUse, ToolUseID: "t0", Name: "whoami"}}},
		{Role: planner.RoleUser, Content: []planner.Block{{Type: planner.BlockToolResult, ToolUseID: "t0", Text: "bob", IsError: true}}},
	},
	Tools: []app.ToolSpec{{Name: "whoami", Description: "who"}},
}

func TestStream_TextAndToolUseThroughTheGateway(t *testing.T) {
	var got map[string]any
	var auth string
	client, ring := setup(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, anthropic.Version, r.Header.Get("anthropic-version"))
		auth = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &got))
		sse(w,
			`{"type":"message_start","message":{"usage":{"input_tokens":20,"output_tokens":1,"cache_read_input_tokens":5}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me "}}`,
			`{"type":"ping"}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"check."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"list_items","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"state\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"ready\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":17}}`,
			`{"type":"message_stop"}`,
		)
	})

	var streamed []string
	resp, err := client.Stream(t.Context(), request, func(s string) { streamed = append(streamed, s) })
	require.NoError(t, err)
	assert.Equal(t, []string{"Let me ", "check."}, streamed)
	assert.Equal(t, "tool_use", resp.StopReason)
	assert.Equal(t, planner.Usage{InputTokens: 20, OutputTokens: 17, CacheReadTokens: 5}, resp.Usage)
	require.Len(t, resp.Content, 2)
	assert.Equal(t, "Let me check.", resp.Content[0].Text)
	assert.Equal(t, planner.BlockToolUse, resp.Content[1].Type)
	assert.Equal(t, "t1", resp.Content[1].ToolUseID)
	assert.JSONEq(t, `{"state":"ready"}`, string(resp.Content[1].Input))

	assert.Equal(t, true, got["stream"])
	assert.Equal(t, "Be brief.", got["system"])
	msgs := got["messages"].([]any)
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"type": "tool_result", "tool_use_id": "t0", "content": "bob", "is_error": true}, result)
	tool := got["tools"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"type": "object"}, tool["input_schema"])

	claims, err := runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now).Verify(t.Context(), auth, "gateway")
	require.NoError(t, err)
	assert.Equal(t, runtoken.KindPlanner, claims.Kind)
	assert.Equal(t, "WEB", claims.Project)
	assert.Equal(t, "bob", claims.ActingFor)
	assert.True(t, claims.Can(runtoken.CapLLMInvoke))
}

func TestStream_Errors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"http error": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"permission_error","message":"no credential"}}`))
		},
		"stream error": func(w http.ResponseWriter, _ *http.Request) {
			sse(w, `{"type":"message_start","message":{"usage":{}}}`, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
		},
		"truncated": func(w http.ResponseWriter, _ *http.Request) {
			sse(w, `{"type":"message_start","message":{"usage":{}}}`)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			client, _ := setup(t, h)
			_, err := client.Stream(t.Context(), request, func(string) {})
			assert.Error(t, err)
		})
	}
	client, _ := setup(t, cases["http error"])
	_, err := client.Stream(t.Context(), request, func(string) {})
	assert.ErrorContains(t, err, "permission_error: no credential")
}
