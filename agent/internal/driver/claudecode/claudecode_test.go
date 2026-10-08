package claudecode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/agent/internal/driver/claudecode"
	"github.com/denyszorinets/ballet/kit/runnerproto"
)

func session() runnerproto.Session {
	return runnerproto.Session{
		Runtime: claudecode.Name, Prompt: "Do it", Instructions: "Be good",
		Skills: []runnerproto.Skill{{Name: "tdd", Description: "Test\nfirst", Body: "Write tests.",
			Files: map[string]string{"ref/a.md": "A"}}},
		MCP:   []runnerproto.MCPServer{{Name: "tracker", URL: "http://core/mcp/tracker"}},
		Model: "claude-x", MaxTurns: 7, LLMURL: "http://gw", TokenEnv: "BALLET_RUN_TOKEN",
	}
}

func TestSetup_WritesSkillsInstructionsAndMCPIntoHome(t *testing.T) {
	s, err := claudecode.Driver{}.Setup(session())
	require.NoError(t, err)
	assert.Equal(t, "Be good", s.Files[".claude/CLAUDE.md"])
	assert.Equal(t, "---\nname: tdd\ndescription: Test first\n---\n\nWrite tests.", s.Files[".claude/skills/tdd/SKILL.md"])
	assert.Equal(t, "A", s.Files[".claude/skills/tdd/ref/a.md"])
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(s.Files[".claude.json"]), &cfg))
	assert.Equal(t, "http://core/mcp/tracker", cfg.MCPServers["tracker"].URL)
	assert.Equal(t, "Bearer ${BALLET_RUN_TOKEN}", cfg.MCPServers["tracker"].Headers["Authorization"])
}

func TestSetup_RunsClaudeStreamingWithTheGateway(t *testing.T) {
	s, err := claudecode.Driver{Command: "/opt/claude"}.Setup(session())
	require.NoError(t, err)
	assert.Equal(t, []string{"/opt/claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--permission-mode", "bypassPermissions", "--model", "claude-x", "--max-turns", "7"}, s.Command)
	assert.Equal(t, "http://gw", s.Env["ANTHROPIC_BASE_URL"])
	assert.Equal(t, "${BALLET_RUN_TOKEN}", s.Env["ANTHROPIC_API_KEY"])
}

func TestSetup_RejectsIncompleteSessions(t *testing.T) {
	for name, mut := range map[string]func(*runnerproto.Session){
		"no prompt":      func(s *runnerproto.Session) { s.Prompt = " " },
		"no gateway":     func(s *runnerproto.Session) { s.LLMURL = "" },
		"no token":       func(s *runnerproto.Session) { s.TokenEnv = "" },
		"escaping skill": func(s *runnerproto.Session) { s.Skills[0].Files = map[string]string{"../x": ""} },
		"bad skill name": func(s *runnerproto.Session) { s.Skills[0].Name = "../x" },
		"absolute file":  func(s *runnerproto.Session) { s.Skills[0].Files = map[string]string{"/etc/x": ""} },
	} {
		t.Run(name, func(t *testing.T) {
			s := session()
			mut(&s)
			_, err := claudecode.Driver{}.Setup(s)
			assert.Error(t, err)
		})
	}
}

func TestMessage_IsAStreamJSONUserMessage(t *testing.T) {
	line := claudecode.Driver{}.Message("hello\nworld")
	require.Equal(t, byte('\n'), line[len(line)-1])
	var m struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	require.NoError(t, json.Unmarshal(line, &m))
	assert.Equal(t, "user", m.Type)
	assert.Equal(t, "user", m.Message.Role)
	assert.Equal(t, "hello\nworld", m.Message.Content)
}

func TestParse_NormalizesEvents(t *testing.T) {
	d := claudecode.Driver{}
	tests := []struct {
		name   string
		line   string
		events []runnerproto.Event
		result *runnerproto.Result
	}{
		{name: "init has no events", line: `{"type":"system","subtype":"init","session_id":"s"}`},
		{name: "not json is ignored", line: `warming up`},
		{
			name: "assistant text and tool use",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"Looking"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
			events: []runnerproto.Event{{Kind: "text", Text: "Looking"},
				{Kind: "tool_use", Tool: "Bash", Input: `{"command":"ls"}`}},
		},
		{
			name:   "tool result as text",
			line:   `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"a.go","is_error":false}]}}`,
			events: []runnerproto.Event{{Kind: "tool_result", Text: "a.go"}},
		},
		{
			name:   "tool result as blocks, failed",
			line:   `{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"boom"}],"is_error":true}]}}`,
			events: []runnerproto.Event{{Kind: "tool_result", Text: "boom", Error: true}},
		},
		{
			name:   "result",
			line:   `{"type":"result","subtype":"success","is_error":false,"result":"Done.","num_turns":4,"total_cost_usd":0.5}`,
			events: []runnerproto.Event{{Kind: "result", Text: "Done."}},
			result: &runnerproto.Result{Success: true, Summary: "Done.", Turns: 4, CostUSD: 0.5},
		},
		{
			name:   "failed result",
			line:   `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"","num_turns":9}`,
			events: []runnerproto.Event{{Kind: "result", Text: "", Error: true}},
			result: &runnerproto.Result{Success: false, Turns: 9},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := d.Parse([]byte(tt.line))
			assert.Equal(t, tt.events, p.Events)
			assert.Equal(t, tt.result, p.Result)
		})
	}
}

func TestParse_TruncatesLongToolResults(t *testing.T) {
	long := make([]byte, 10_000)
	for i := range long {
		long[i] = 'x'
	}
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_result", "content": string(long)}}}})
	p := claudecode.Driver{}.Parse(line)
	require.Len(t, p.Events, 1)
	assert.Less(t, len(p.Events[0].Text), 5000)
}

func TestInterrupt_IsAControlRequest(t *testing.T) {
	var m struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	require.NoError(t, json.Unmarshal(claudecode.Driver{}.Interrupt(), &m))
	assert.Equal(t, "control_request", m.Type)
	assert.NotEmpty(t, m.RequestID)
	assert.Equal(t, "interrupt", m.Request.Subtype)
}

func TestParse_KeepsTheSessionID(t *testing.T) {
	p := claudecode.Driver{}.Parse([]byte(`{"type":"system","subtype":"init","session_id":"abc-1"}`))
	assert.Equal(t, "abc-1", p.SessionID)
}

func TestResume_RestoresTheTranscriptAndResumes(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-tmp-ws-repo")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "abc-1.jsonl"), []byte("{\"x\":1}\n"), 0o644))
	d := claudecode.Driver{}
	state, err := d.State(home, "abc-1")
	require.NoError(t, err)
	_, err = d.State(home, "other")
	assert.Error(t, err)
	_, err = d.State(home, "../x")
	assert.Error(t, err)

	s := session()
	s.Resume = &runnerproto.Resume{SessionID: "abc-1", State: state}
	setup, err := d.Setup(s)
	require.NoError(t, err)
	assert.Equal(t, "{\"x\":1}\n", setup.Files[".claude/projects/ballet-resumed/abc-1.jsonl"])
	assert.Equal(t, []string{"--resume", "abc-1"}, setup.Command[len(setup.Command)-2:])

	s.Resume.SessionID = "../../etc"
	_, err = d.Setup(s)
	assert.Error(t, err)
}
