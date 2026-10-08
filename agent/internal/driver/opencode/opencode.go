// Package opencode drives opencode through the Agent Client Protocol
// (opencode acp): JSON-RPC over standard input and output, one session
// per process, kept open between turns (ADR-0025, ADR-0026).
package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/denyszorinets/ballet/agent/internal/driver"
	"github.com/denyszorinets/ballet/kit/agentproto"
)

// Name of the runtime.
const Name = "opencode"

// DefaultModel is the model of sessions that name none.
const DefaultModel = "claude-sonnet-5-5"

// maxToolResult bounds a tool result kept as an event.
const maxToolResult = 4000

// Driver drives opencode.
type Driver struct {
	Command string // the opencode executable (default "opencode")
}

// Setup returns the session's files, environment and command. The
// configuration, instructions and skills are opencode's global ones in
// HOME — outside the repository, so agents cannot commit them.
func (d Driver) Setup(s agentproto.Session) (driver.Setup, error) {
	if strings.TrimSpace(s.Prompt) == "" {
		return driver.Setup{}, errors.New("opencode: the session has no prompt")
	}
	if s.LLMURL == "" || s.TokenEnv == "" {
		return driver.Setup{}, errors.New("opencode: the gateway URL and the token variable are required")
	}
	files, err := driver.SkillFiles(".config/opencode/skills", s.Skills)
	if err != nil {
		return driver.Setup{}, fmt.Errorf("opencode: %w", err)
	}
	if s.Instructions != "" {
		files[".config/opencode/AGENTS.md"] = s.Instructions
	}
	model := s.Model
	if model == "" {
		model = DefaultModel
	}
	token := "{env:" + s.TokenEnv + "}"
	mcp := map[string]any{}
	for _, m := range s.MCP {
		mcp[m.Name] = map[string]any{"type": "remote", "url": m.URL, "headers": map[string]string{"Authorization": "Bearer " + token}}
	}
	cfg, err := json.MarshalIndent(map[string]any{
		"$schema": "https://opencode.ai/config.json",
		// The LLM gateway speaks the Anthropic API; the model is declared
		// so opencode accepts models its catalog does not know.
		"provider": map[string]any{"anthropic": map[string]any{
			"options": map[string]string{"baseURL": strings.TrimSuffix(s.LLMURL, "/") + "/v1", "apiKey": token},
			"models":  map[string]any{model: map[string]any{}},
		}},
		"model": "anthropic/" + model,
		"mcp":   mcp,
		// Sessions run in the agent's container or VM, the isolation
		// boundary (ADR-0025).
		"permission": "allow",
		"autoupdate": false,
		"share":      "disabled",
	}, "", "  ")
	if err != nil {
		return driver.Setup{}, err
	}
	files[".config/opencode/opencode.json"] = string(cfg)
	command := d.Command
	if command == "" {
		command = "opencode"
	}
	return driver.Setup{Files: files, Command: []string{command, "acp"}, Env: map[string]string{
		"OPENCODE_DISABLE_AUTOUPDATE": "1",
	}}, nil
}

// State is not kept: a parked opencode session's stage starts a new
// session with the answers.
func (Driver) State(string, string) ([]byte, error) {
	return nil, errors.New("opencode: sessions are not resumed")
}

// NewCodec returns the codec of a session working in dir.
func (Driver) NewCodec(dir string) driver.Codec { return &codec{dir: dir, calls: map[string]*call{}} }

// call is a tool call: opencode names it first and sends its input with
// the first progress update.
type call struct {
	name, input string
	shown       bool
}

// codec speaks ACP: initialize, session/new, then one session/prompt per
// turn; session/update notifications stream the turn.
type codec struct {
	dir     string
	nextID  int64
	initID  int64
	newID   int64
	prompt  int64  // the running prompt request; 0: none
	pending string // the first prompt, sent once the session exists
	session string
	text    strings.Builder // the agent's current message, in chunks
	last    string          // the agent's last message of the turn
	calls   map[string]*call
	cost    float64 // the session's so far
}

type message struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      *int64    `json:"id,omitempty"`
	Method  string    `json:"method,omitempty"`
	Params  any       `json:"params,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func encode(m message) []byte {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	return append(b, '\n')
}

func (c *codec) request(method string, params any) ([]byte, int64) {
	c.nextID++
	id := c.nextID
	return encode(message{ID: &id, Method: method, Params: params}), id
}

// Start initializes the connection; the prompt follows once the session
// exists.
func (c *codec) Start(prompt string) []byte {
	c.pending = prompt
	b, id := c.request("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}})
	c.initID = id
	return b
}

// Message starts the next turn.
func (c *codec) Message(text string) []byte {
	b, id := c.request("session/prompt", map[string]any{"sessionId": c.session,
		"prompt": []any{map[string]any{"type": "text", "text": text}}})
	c.prompt = id
	return b
}

// Interrupt cancels the running turn (a notification).
func (c *codec) Interrupt() []byte {
	if c.session == "" {
		return nil
	}
	return encode(message{Method: "session/cancel", Params: map[string]any{"sessionId": c.session}})
}

// incoming is a message opencode wrote.
type incoming struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// Parse handles one message.
func (c *codec) Parse(line []byte) driver.Parsed {
	var m incoming
	if json.Unmarshal(line, &m) != nil {
		return driver.Parsed{}
	}
	switch {
	case m.Method == "session/update":
		return c.update(m.Params)
	case m.Method == "session/request_permission" && m.ID != nil:
		return driver.Parsed{Reply: c.allow(*m.ID, m.Params)}
	case m.Method != "" && m.ID != nil:
		// A request the client does not serve.
		return driver.Parsed{Reply: encode(message{ID: m.ID, Error: &rpcError{Code: -32601, Message: "method not found"}})}
	case m.ID == nil:
		return driver.Parsed{}
	}
	switch *m.ID {
	case c.initID:
		b, id := c.request("session/new", map[string]any{"cwd": c.dir, "mcpServers": []any{}})
		c.newID = id
		return driver.Parsed{Reply: b}
	case c.newID:
		var r struct {
			SessionID string `json:"sessionId"`
		}
		if m.Error != nil || json.Unmarshal(m.Result, &r) != nil || r.SessionID == "" {
			return c.end(false, "opencode could not start a session: "+errText(m.Error))
		}
		c.session = r.SessionID
		return driver.Parsed{SessionID: r.SessionID, Reply: c.Message(c.pending)}
	case c.prompt:
		if m.Error != nil {
			return c.end(false, m.Error.Message)
		}
		var r struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(m.Result, &r)
		return c.end(r.StopReason == "end_turn", "")
	}
	return driver.Parsed{}
}

// end ends a turn: the agent's last message (or text) is its result.
func (c *codec) end(success bool, text string) driver.Parsed {
	p := driver.Parsed{Events: c.flush()}
	c.prompt = 0
	if text == "" {
		text = c.last
	}
	c.last = ""
	p.Events = append(p.Events, agentproto.Event{Kind: agentproto.EventResult, Text: text, Error: !success})
	// One turn per result; the agent adds them up.
	p.Result = &agentproto.Result{Success: success, Summary: text, Turns: 1, CostUSD: c.cost}
	return p
}

// flush turns the agent's buffered message into a text event.
func (c *codec) flush() []agentproto.Event {
	if c.text.Len() == 0 {
		return nil
	}
	t := c.text.String()
	c.text.Reset()
	c.last = t
	return []agentproto.Event{{Kind: agentproto.EventText, Text: t}}
}

func (c *codec) update(params json.RawMessage) driver.Parsed {
	var p struct {
		Update struct {
			Kind    string          `json:"sessionUpdate"`
			CallID  string          `json:"toolCallId"`
			Content json.RawMessage `json:"content"`
			Title   string          `json:"title"`
			Status  string          `json:"status"`
			Input   json.RawMessage `json:"rawInput"`
			Cost    struct {
				Amount float64 `json:"amount"`
			} `json:"cost"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return driver.Parsed{}
	}
	u := p.Update
	switch u.Kind {
	case "agent_message_chunk":
		var chunk struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &chunk) == nil && chunk.Type == "text" {
			c.text.WriteString(chunk.Text)
		}
	case "tool_call":
		c.calls[u.CallID] = &call{name: u.Title}
	case "tool_call_update":
		cl := c.calls[u.CallID]
		if cl == nil {
			cl = &call{}
			c.calls[u.CallID] = cl
		}
		if len(u.Input) > 0 && string(u.Input) != "null" {
			cl.input = string(u.Input)
		}
		var events []agentproto.Event
		if !cl.shown && cl.name != "" {
			cl.shown = true
			events = append(c.flush(), agentproto.Event{Kind: agentproto.EventToolUse, Tool: cl.name, Input: cl.input})
		}
		if u.Status == "completed" || u.Status == "failed" {
			delete(c.calls, u.CallID)
			events = append(append(events, c.flush()...), agentproto.Event{Kind: agentproto.EventToolResult,
				Text: truncate(contentText(u.Content), maxToolResult), Error: u.Status == "failed"})
		}
		return driver.Parsed{Events: events}
	case "usage_update":
		c.cost = u.Cost.Amount
	}
	return driver.Parsed{}
}

// allow answers a permission request with its first allowing option.
func (c *codec) allow(id int64, params json.RawMessage) []byte {
	var p struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &p)
	choice := ""
	for _, o := range p.Options {
		if strings.HasPrefix(o.Kind, "allow") {
			choice = o.OptionID
			break
		}
	}
	if choice == "" {
		return encode(message{ID: &id, Result: map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}})
	}
	return encode(message{ID: &id, Result: map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": choice}}})
}

// contentText joins the text of a tool call's content blocks.
func contentText(raw json.RawMessage) string {
	var blocks []struct {
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Content.Type == "text" {
			parts = append(parts, b.Content.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func errText(e *rpcError) string {
	if e == nil {
		return "no session ID"
	}
	return e.Message
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
