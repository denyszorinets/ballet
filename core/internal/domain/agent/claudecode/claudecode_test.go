package claudecode_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/agent"
	"github.com/denyszorinets/ballet/core/internal/domain/agent/claudecode"
)

var adapter = claudecode.Adapter{GatewayURL: "http://gw:8082", APIKeyEnv: "BALLET_RUN_TOKEN"}

func TestBuild(t *testing.T) {
	b, err := adapter.Build(agent.Session{
		Prompt: "Implement WEB-1", Instructions: "You implement.", Model: "claude-x", MaxTurns: 50,
		Skills: []agent.Skill{{Name: "gitflow", Description: "Branching\nrules", Body: "Use develop.",
			Files: map[string]string{"ref/naming.md": "x"}}},
		MCP: []agent.MCPServer{{Name: "knowledge", URL: "http://kn/mcp", TokenEnv: "BALLET_RUN_TOKEN"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Implement WEB-1", b.Files[".ballet/prompt.md"])
	assert.Equal(t, "You implement.", b.Files[".home/.claude/CLAUDE.md"])
	assert.Equal(t, "---\nname: gitflow\ndescription: Branching rules\n---\n\nUse develop.", b.Files[".home/.claude/skills/gitflow/SKILL.md"])
	assert.Equal(t, "x", b.Files[".home/.claude/skills/gitflow/ref/naming.md"])

	var mcp map[string]map[string]map[string]any
	require.NoError(t, json.Unmarshal([]byte(b.Files[".home/.claude.json"]), &mcp))
	assert.Equal(t, "http://kn/mcp", mcp["mcpServers"]["knowledge"]["url"])
	assert.Equal(t, map[string]any{"Authorization": "Bearer ${BALLET_RUN_TOKEN}"}, mcp["mcpServers"]["knowledge"]["headers"],
		"the token is referenced, not written")

	require.Equal(t, []string{"sh", "-c"}, b.Command[:2])
	assert.Contains(t, b.Command[2], `export ANTHROPIC_API_KEY="${BALLET_RUN_TOKEN}"`)
	assert.Contains(t, b.Command[2], `exec 'claude' -p --output-format stream-json --verbose --permission-mode bypassPermissions --model 'claude-x' --max-turns 50 < "$BALLET_WORKSPACE/.ballet/prompt.md"`)
	assert.Equal(t, "http://gw:8082", b.Env["ANTHROPIC_BASE_URL"])
	assert.NotContains(t, b.Env, "ANTHROPIC_API_KEY")
	assert.Equal(t, "1", b.Env["IS_SANDBOX"])

	_, err = adapter.Build(agent.Session{Prompt: " "})
	assert.Error(t, err)
	_, err = adapter.Build(agent.Session{Prompt: "x", Skills: []agent.Skill{{Name: "s", Files: map[string]string{"../x": ""}}}})
	assert.Error(t, err)
}

func TestResult(t *testing.T) {
	out := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"result"}]}}
{"type":"result","subtype":"success","is_error":false,"num_turns":3,"result":"Done: added login.","total_cost_usd":0.25}
`
	r, ok := adapter.Result(out)
	require.True(t, ok)
	assert.Equal(t, agent.Result{Success: true, Summary: "Done: added login.", Turns: 3, CostUSD: 0.25}, r)

	r, ok = adapter.Result(`{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":50,"result":""}`)
	require.True(t, ok)
	assert.False(t, r.Success)

	_, ok = adapter.Result("panic: something\n")
	assert.False(t, ok)
}
