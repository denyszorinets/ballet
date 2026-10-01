package fakeprovider_test

import (
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

	out = post(t, p, `{"stream":true,"messages":[{"role":"user","content":"/tool x {}"}]}`)
	assert.Contains(t, out, `"text":"hel"`, "without tools the fake answers as before")
}
