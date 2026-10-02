package fakeprovider_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/gateway/internal/fakeprovider"
)

func post(t *testing.T, p *fakeprovider.Provider, body string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, p.URL+"/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", p.Key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return string(out)
}

func TestScriptedToolUse(t *testing.T) {
	p := fakeprovider.New(t, "k")
	out := post(t, p, `{"stream":true,"tools":[{"name":"list_items"}],
		"messages":[{"role":"user","content":[{"type":"text","text":"/tool list_items {\"state\":\"ready\"}"}]}]}`)
	assert.Contains(t, out, `"name":"list_items"`)
	assert.Contains(t, out, `"partial_json":"{\"state\":\"ready\"}"`)
	assert.Contains(t, out, `"stop_reason":"tool_use"`)

	out = post(t, p, `{"stream":true,"tools":[{"name":"list_items"}],
		"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_fake","content":"WEB-1 Login"}]}]}`)
	assert.Contains(t, out, "Tool result: WEB-1 Login")
	assert.Contains(t, out, `"stop_reason":"end_turn"`)

	out = post(t, p, `{"stream":true,"tools":[{"name":"x"}],"messages":[{"role":"user","content":[{"type":"text","text":"# Task\n\nContext.\n/tool x {\"a\":1}\n"}]}]}`)
	assert.Contains(t, out, `"name":"x"`, "a /tool line inside a longer message")

	out = post(t, p, `{"stream":true,"messages":[{"role":"user","content":"/tool x {}"}]}`)
	assert.Contains(t, out, `"text":"hel"`, "without tools the fake answers as before")
}

func TestScriptedConversationAsAgentsSendIt(t *testing.T) {
	p := fakeprovider.New(t, "k")
	// The command, the agent's environment message after it, the call, its
	// result, and another agent message after the result.
	conv := `{"stream":true,"tools":[{"name":"x"}],"messages":[
		{"role":"user","content":[{"type":"text","text":"# Task\n/tool x {}"}]},
		{"role":"user","content":[{"type":"text","text":"# Environment"}]}%s]}`
	out := post(t, p, fmt.Sprintf(conv, ""))
	assert.Contains(t, out, `"name":"x"`)
	out = post(t, p, fmt.Sprintf(conv, `,
		{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"x","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"done"}]}]},
		{"role":"user","content":[{"type":"text","text":"<system-reminder>…</system-reminder>"}]}`))
	assert.Contains(t, out, "Tool result: done", "answered once the result is there, wherever the last message is")
}
