// Package agent describes coding-agent sessions independent of the agent
// runtime (ADR-0003): what a session gets — task, instructions, skills, MCP
// servers. Agents run sessions through their driver for the runtime
// (ADR-0025) and report the result.
package agent

// ClaudeCode is the Claude Code runtime.
const ClaudeCode = "claude-code"

// Runtimes are the runtimes agents can drive.
var Runtimes = []string{ClaudeCode}

// Skill is a skill as a session receives it.
type Skill struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Body        string            `json:"body"`            // SKILL.md body
	Files       map[string]string `json:"files,omitempty"` // supporting files, relative to the skill
}

// MCPServer is an MCP server the session may use, authenticated with the
// run token.
type MCPServer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Session is everything an agent session starts with.
type Session struct {
	Runtime      string      `json:"runtime"`
	Prompt       string      `json:"prompt"`                 // the task
	Instructions string      `json:"instructions,omitempty"` // standing instructions (role, stage, conventions)
	Skills       []Skill     `json:"skills,omitempty"`
	MCP          []MCPServer `json:"mcp,omitempty"`
	Model        string      `json:"model,omitempty"` // "": the runtime's default
	MaxTurns     int         `json:"max_turns,omitempty"`
	LLMURL       string      `json:"llm_url"`       // the LLM gateway as reached from the session
	TokenEnv     string      `json:"token_env"`     // secret variable with the run token
	Dir          string      `json:"dir,omitempty"` // where the session works, relative to the workspace
	// Resume, when set, continues a parked session (ADR-0026); Prompt is
	// then its next message.
	Resume *Resume `json:"resume,omitempty"`
}

// Resume is a parked session to continue. State is not stored with the
// run that resumes it: Core attaches it when the run starts.
type Resume struct {
	RunID     string `json:"run_id"` // the parked run
	SessionID string `json:"session_id"`
	State     []byte `json:"-"`
}
