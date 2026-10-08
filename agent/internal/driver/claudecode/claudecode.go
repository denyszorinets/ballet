// Package claudecode drives Claude Code: claude -p with stream-json input
// and output, so the session stays open between turns (ADR-0026).
package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/agent/internal/driver"
	"github.com/denyszorinets/ballet/kit/agentproto"
)

// Name of the runtime.
const Name = "claude-code"

// maxToolResult bounds a tool result kept as an event.
const maxToolResult = 4000

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)

// Driver drives Claude Code.
type Driver struct {
	Command string // the claude executable (default "claude")
}

// Setup returns the session's files, environment and command. Skills,
// instructions and MCP servers are Claude Code's user-level configuration
// in HOME — outside the repository, so agents cannot commit them.
func (d Driver) Setup(s agentproto.Session) (driver.Setup, error) {
	if strings.TrimSpace(s.Prompt) == "" {
		return driver.Setup{}, errors.New("claude-code: the session has no prompt")
	}
	if s.LLMURL == "" || s.TokenEnv == "" {
		return driver.Setup{}, errors.New("claude-code: the gateway URL and the token variable are required")
	}
	files, err := driver.SkillFiles(".claude/skills", s.Skills)
	if err != nil {
		return driver.Setup{}, fmt.Errorf("claude-code: %w", err)
	}
	if s.Instructions != "" {
		files[".claude/CLAUDE.md"] = s.Instructions
	}
	if len(s.MCP) > 0 {
		servers := map[string]any{}
		for _, m := range s.MCP {
			// Claude Code expands ${VAR}: the token stays in the environment.
			servers[m.Name] = map[string]any{"type": "http", "url": m.URL,
				"headers": map[string]string{"Authorization": "Bearer ${" + s.TokenEnv + "}"}}
		}
		cfg, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
		if err != nil {
			return driver.Setup{}, err
		}
		files[".claude.json"] = string(cfg)
	}

	command := d.Command
	if command == "" {
		command = "claude"
	}
	args := []string{command, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--permission-mode", "bypassPermissions"}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(s.MaxTurns))
	}
	if s.Resume != nil {
		if !sessionIDRe.MatchString(s.Resume.SessionID) {
			return driver.Setup{}, fmt.Errorf("claude-code: invalid session ID %q", s.Resume.SessionID)
		}
		// Claude Code finds a session by its ID in any project directory.
		files[".claude/projects/ballet-resumed/"+s.Resume.SessionID+".jsonl"] = string(s.Resume.State)
		args = append(args, "--resume", s.Resume.SessionID)
	}
	return driver.Setup{
		Files:   files,
		Command: args,
		Env: map[string]string{
			"ANTHROPIC_BASE_URL": s.LLMURL,
			"ANTHROPIC_API_KEY":  "${" + s.TokenEnv + "}",
			// Sessions run in the agent's container or VM, the isolation
			// boundary (ADR-0025), so Claude Code may skip permission
			// prompts there.
			"IS_SANDBOX":          "1",
			"DISABLE_AUTOUPDATER": "1",
			"DISABLE_TELEMETRY":   "1",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		},
	}, nil
}

// NewCodec returns a codec: Claude Code's stream-json needs no session
// state.
func (Driver) NewCodec(string) driver.Codec { return codec{} }

type codec struct{}

// Start sends the prompt as the first message.
func (c codec) Start(prompt string) []byte { return c.Message(prompt) }

// Message encodes a user message.
func (codec) Message(text string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	return append(b, '\n')
}

// Interrupt encodes a control request stopping the current turn.
func (codec) Interrupt() []byte {
	b, _ := json.Marshal(map[string]any{"type": "control_request",
		"request_id": fmt.Sprintf("ballet-%d", time.Now().UnixNano()), "request": map[string]any{"subtype": "interrupt"}})
	return append(b, '\n')
}

// line is the part of a stream-json line the driver reads.
type line struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Message   struct {
		Content []struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			Content json.RawMessage `json:"content"`
			IsError bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
	IsError bool    `json:"is_error"`
	Result  string  `json:"result"`
	Turns   int     `json:"num_turns"`
	Cost    float64 `json:"total_cost_usd"`
}

// Parse normalizes one line of output; other lines are ignored.
func (codec) Parse(b []byte) driver.Parsed {
	var l line
	if json.Unmarshal(b, &l) != nil {
		return driver.Parsed{}
	}
	p := driver.Parsed{SessionID: l.SessionID}
	switch l.Type {
	case "assistant":
		for _, c := range l.Message.Content {
			switch c.Type {
			case "text":
				p.Events = append(p.Events, agentproto.Event{Kind: agentproto.EventText, Text: c.Text})
			case "tool_use":
				p.Events = append(p.Events, agentproto.Event{Kind: agentproto.EventToolUse, Tool: c.Name, Input: string(c.Input)})
			}
		}
	case "user":
		for _, c := range l.Message.Content {
			if c.Type == "tool_result" {
				p.Events = append(p.Events, agentproto.Event{Kind: agentproto.EventToolResult,
					Text: truncate(toolText(c.Content), maxToolResult), Error: c.IsError})
			}
		}
	case "result":
		ok := l.Subtype == "success" && !l.IsError
		p.Events = []agentproto.Event{{Kind: agentproto.EventResult, Text: l.Result, Error: !ok}}
		p.Result = &agentproto.Result{Success: ok, Summary: l.Result, Turns: l.Turns, CostUSD: l.Cost}
	}
	return p
}

// State reads the session's transcript: Claude Code keeps it as
// .claude/projects/<directory>/<session ID>.jsonl in HOME.
func (Driver) State(home, sessionID string) ([]byte, error) {
	if !sessionIDRe.MatchString(sessionID) {
		return nil, fmt.Errorf("claude-code: invalid session ID %q", sessionID)
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("claude-code: no transcript of session %s", sessionID)
	}
	return os.ReadFile(matches[0])
}

// toolText is a tool result's content: a string or text blocks.
func toolText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
