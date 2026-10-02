// Package agent describes coding-agent sessions independent of the agent
// runtime (ADR-0003): what a session gets — task, instructions, skills, MCP
// servers — and what it reports. Adapters turn a Session into the files,
// environment and command a Runner executes, and read the result back.
package agent

// Skill is a skill as a session receives it.
type Skill struct {
	Name        string
	Description string
	Body        string            // SKILL.md body
	Files       map[string]string // supporting files, relative to the skill
}

// MCPServer is an MCP server the session may use, authenticated with the
// bearer token in environment variable TokenEnv.
type MCPServer struct {
	Name     string
	URL      string
	TokenEnv string
}

// Session is everything an agent session starts with.
type Session struct {
	Prompt       string // the task
	Instructions string // standing instructions (role, stage, conventions)
	Skills       []Skill
	MCP          []MCPServer
	Model        string // "": the runtime's default
	MaxTurns     int    // 0: unlimited
}

// Built is a session as the Runner executes it.
type Built struct {
	Command []string
	Files   map[string]string // workspace-relative path → content
	Env     map[string]string
}

// Result is what a session reported at its end.
type Result struct {
	Success bool
	Summary string // the agent's final message
	Turns   int
	CostUSD float64
}

// Adapter builds sessions for one agent runtime and reads their results.
type Adapter interface {
	Name() string
	Build(s Session) (Built, error)
	// Result extracts the result from the session's standard output; ok is
	// false when the output holds none (the session crashed).
	Result(stdout string) (r Result, ok bool)
}
