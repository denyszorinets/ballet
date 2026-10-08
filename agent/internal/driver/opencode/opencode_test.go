package opencode_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/agent/internal/driver"
	"github.com/denyszorinets/ballet/agent/internal/driver/opencode"
	"github.com/denyszorinets/ballet/kit/runnerproto"
)

func session() runnerproto.Session {
	return runnerproto.Session{
		Runtime: opencode.Name, Prompt: "Do it", Instructions: "Be good",
		Skills: []runnerproto.Skill{{Name: "tdd", Description: "Test\nfirst", Body: "Write tests.",
			Files: map[string]string{"ref/a.md": "A"}}},
		MCP:   []runnerproto.MCPServer{{Name: "tracker", URL: "http://core/mcp/tracker"}},
		Model: "claude-x", LLMURL: "http://gw", TokenEnv: "BALLET_RUN_TOKEN",
	}
}

func TestSetup_ConfiguresTheGatewaySkillsAndMCPInHome(t *testing.T) {
	s, err := opencode.Driver{Command: "/opt/opencode"}.Setup(session())
	require.NoError(t, err)
	assert.Equal(t, []string{"/opt/opencode", "acp"}, s.Command)
	assert.Equal(t, "Be good", s.Files[".config/opencode/AGENTS.md"])
	assert.Equal(t, "---\nname: tdd\ndescription: Test first\n---\n\nWrite tests.", s.Files[".config/opencode/skills/tdd/SKILL.md"])
	assert.Equal(t, "A", s.Files[".config/opencode/skills/tdd/ref/a.md"])
	var cfg struct {
		Model    string `json:"model"`
		Provider map[string]struct {
			Options map[string]string `json:"options"`
			Models  map[string]any    `json:"models"`
		} `json:"provider"`
		MCP map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcp"`
		Permission string `json:"permission"`
	}
	require.NoError(t, json.Unmarshal([]byte(s.Files[".config/opencode/opencode.json"]), &cfg))
	assert.Equal(t, "anthropic/claude-x", cfg.Model)
	assert.Equal(t, "http://gw/v1", cfg.Provider["anthropic"].Options["baseURL"])
	assert.Equal(t, "{env:BALLET_RUN_TOKEN}", cfg.Provider["anthropic"].Options["apiKey"])
	assert.Contains(t, cfg.Provider["anthropic"].Models, "claude-x", "unknown models are accepted")
	assert.Equal(t, "remote", cfg.MCP["tracker"].Type)
	assert.Equal(t, "Bearer {env:BALLET_RUN_TOKEN}", cfg.MCP["tracker"].Headers["Authorization"])
	assert.Equal(t, "allow", cfg.Permission, "the container is the boundary")
}

func TestSetup_RejectsIncompleteSessions(t *testing.T) {
	for name, mut := range map[string]func(*runnerproto.Session){
		"no prompt":      func(s *runnerproto.Session) { s.Prompt = "" },
		"no gateway":     func(s *runnerproto.Session) { s.LLMURL = "" },
		"bad skill name": func(s *runnerproto.Session) { s.Skills[0].Name = "../x" },
		"escaping file":  func(s *runnerproto.Session) { s.Skills[0].Files = map[string]string{"../x": ""} },
	} {
		t.Run(name, func(t *testing.T) {
			s := session()
			mut(&s)
			_, err := opencode.Driver{}.Setup(s)
			assert.Error(t, err)
		})
	}
}

// rpc decodes a JSON-RPC message the codec wrote.
type rpc struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

func decode(t *testing.T, b []byte) rpc {
	t.Helper()
	require.True(t, strings.HasSuffix(string(b), "\n"))
	var m rpc
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func line(v any) []byte { b, _ := json.Marshal(v); return b }

func update(u map[string]any) []byte {
	return line(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "s1", "update": u}})
}

// started runs the ACP handshake; it returns the codec and the prompt's
// request ID.
func started(t *testing.T) (driver.Codec, int64) {
	t.Helper()
	c := opencode.Driver{}.NewCodec("/ws/repo")
	init := decode(t, c.Start("Do it"))
	assert.Equal(t, "initialize", init.Method)
	p := c.Parse(line(map[string]any{"jsonrpc": "2.0", "id": *init.ID, "result": map[string]any{"protocolVersion": 1}}))
	newSession := decode(t, p.Reply)
	require.Equal(t, "session/new", newSession.Method)
	assert.JSONEq(t, `{"cwd":"/ws/repo","mcpServers":[]}`, string(newSession.Params))
	p = c.Parse(line(map[string]any{"jsonrpc": "2.0", "id": *newSession.ID, "result": map[string]any{"sessionId": "s1"}}))
	assert.Equal(t, "s1", p.SessionID)
	prompt := decode(t, p.Reply)
	require.Equal(t, "session/prompt", prompt.Method)
	assert.JSONEq(t, `{"sessionId":"s1","prompt":[{"type":"text","text":"Do it"}]}`, string(prompt.Params))
	return c, *prompt.ID
}

func TestCodec_NormalizesATurn(t *testing.T) {
	c, id := started(t)
	var events []runnerproto.Event
	for _, l := range [][]byte{
		update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Look"}}),
		update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "ing."}}),
		update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "bash", "status": "pending",
			"rawInput": map[string]any{"cwd": "/w"}}),
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "title": "ls", "status": "in_progress",
			"rawInput": map[string]any{"command": "ls"}}),
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "in_progress",
			"rawInput": map[string]any{"command": "ls"}}),
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed",
			"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "a.go"}}}}),
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "status": "failed",
			"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "boom"}}}}),
		update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Done."}}),
		update(map[string]any{"sessionUpdate": "usage_update", "cost": map[string]any{"amount": 0.25, "currency": "USD"}}),
	} {
		p := c.Parse(l)
		require.Nil(t, p.Result)
		events = append(events, p.Events...)
	}
	end := c.Parse(line(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}}))
	events = append(events, end.Events...)
	assert.Equal(t, []runnerproto.Event{
		{Kind: "text", Text: "Looking."},
		{Kind: "tool_use", Tool: "bash", Input: `{"command":"ls"}`},
		{Kind: "tool_result", Text: "a.go"},
		{Kind: "tool_result", Text: "boom", Error: true},
		{Kind: "text", Text: "Done."},
		{Kind: "result", Text: "Done."},
	}, events)
	assert.Equal(t, &runnerproto.Result{Success: true, Summary: "Done.", Turns: 1, CostUSD: 0.25}, end.Result)

	next := decode(t, c.Message("More"))
	assert.Equal(t, "session/prompt", next.Method)
	assert.Greater(t, *next.ID, id)
	failed := c.Parse(line(map[string]any{"jsonrpc": "2.0", "id": *next.ID, "error": map[string]any{"code": -32603,
		"message": "Internal error"}}))
	assert.Equal(t, []runnerproto.Event{{Kind: "result", Text: "Internal error", Error: true}}, failed.Events)
	assert.False(t, failed.Result.Success)
	assert.Equal(t, 1, failed.Result.Turns, "each result is one turn")
}

func TestCodec_InterruptCancelsTheTurn(t *testing.T) {
	assert.Nil(t, opencode.Driver{}.NewCodec("/w").Interrupt(), "no session yet")
	c, id := started(t)
	cancel := decode(t, c.Interrupt())
	assert.Equal(t, "session/cancel", cancel.Method)
	assert.Nil(t, cancel.ID, "a notification")
	assert.JSONEq(t, `{"sessionId":"s1"}`, string(cancel.Params))
	end := c.Parse(line(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "cancelled"}}))
	assert.False(t, end.Result.Success)
}

func TestCodec_AllowsWhatTheAgentAsks(t *testing.T) {
	c, _ := started(t)
	p := c.Parse(line(map[string]any{"jsonrpc": "2.0", "id": 77, "method": "session/request_permission", "params": map[string]any{
		"sessionId": "s1", "options": []any{
			map[string]any{"optionId": "no", "kind": "reject_once"},
			map[string]any{"optionId": "yes", "kind": "allow_once"},
		}}}))
	reply := decode(t, p.Reply)
	assert.Equal(t, int64(77), *reply.ID)
	assert.JSONEq(t, `{"outcome":{"outcome":"selected","optionId":"yes"}}`, string(reply.Result))
}

func TestState_IsNotKept(t *testing.T) {
	_, err := opencode.Driver{}.State(t.TempDir(), "s1")
	assert.Error(t, err, "opencode sessions start afresh after parking")
}
