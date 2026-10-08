// Package runnerproto is the protocol between Core and agents (ADR-0025):
// JSON-RPC over WebSocket (kit/rpc) at Path. The agent connects with its
// token, introduces itself with runner.hello, and then receives run.start
// and run.cancel requests; it reports run.status, run.log and
// run.finished. Method names keep the earlier "runner" prefix.
package runnerproto

// Path is where Core serves the Runner API.
const Path = "/runner/rpc"

// Methods. Runner → Core: Hello, Status, Log, Finished. Core → Runner:
// Start, Cancel.
const (
	MethodHello    = "runner.hello"
	MethodStatus   = "run.status"
	MethodLog      = "run.log"
	MethodFinished = "run.finished"
	MethodStart    = "run.start"
	MethodCancel   = "run.cancel"
)

// Hello introduces a Runner. Active lists runs it is still executing
// (after a reconnect); Core fails runs it assigned to this Runner that are
// not listed.
type Hello struct {
	Runner   string            `json:"runner" msgpack:"runner"`     // stable name, unique per Runner
	Labels   map[string]string `json:"labels" msgpack:"labels"`     // e.g. backend=docker
	Capacity int               `json:"capacity" msgpack:"capacity"` // concurrent runs
	Active   []string          `json:"active" msgpack:"active"`
}

// Spec is what a Runner executes for a run.
type Spec struct {
	Image   string            `json:"image,omitempty" msgpack:"image,omitempty"` // container image (container backends)
	Command []string          `json:"command" msgpack:"command"`
	Env     map[string]string `json:"env,omitempty" msgpack:"env,omitempty"`
	// SecretEnv is added to Env for the session; it holds tokens and must
	// never be logged or persisted by the Runner.
	SecretEnv map[string]string `json:"secret_env,omitempty" msgpack:"secret_env,omitempty"`
	Workdir   string            `json:"workdir,omitempty" msgpack:"workdir,omitempty"` // relative to the run's workspace
	// Files are written into the workspace before the session starts:
	// relative path → content. HOME is <workspace>/.home in every backend.
	Files          map[string]string `json:"files,omitempty" msgpack:"files,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" msgpack:"timeout_seconds,omitempty"` // 0: Runner default
	// Session, when set, is a coding-agent session the agent runs through
	// its driver for Session.Runtime after Command (the workspace
	// preparation; may be empty) succeeded.
	Session *Session `json:"session,omitempty" msgpack:"session,omitempty"`
}

// Session is a coding-agent session, independent of the runtime: the
// agent's driver for Runtime turns it into files, environment and a
// process (ADR-0003, ADR-0025).
type Session struct {
	Runtime      string      `json:"runtime" msgpack:"runtime"` // e.g. "claude-code"
	Prompt       string      `json:"prompt" msgpack:"prompt"`   // the task
	Instructions string      `json:"instructions,omitempty" msgpack:"instructions,omitempty"`
	Skills       []Skill     `json:"skills,omitempty" msgpack:"skills,omitempty"`
	MCP          []MCPServer `json:"mcp,omitempty" msgpack:"mcp,omitempty"`
	Model        string      `json:"model,omitempty" msgpack:"model,omitempty"` // "": the runtime's default
	MaxTurns     int         `json:"max_turns,omitempty" msgpack:"max_turns,omitempty"`
	// LLMURL is the LLM gateway as reached from the session.
	LLMURL string `json:"llm_url" msgpack:"llm_url"`
	// TokenEnv names the secret environment variable holding the run
	// token: the gateway's API key and the MCP servers' bearer token.
	TokenEnv string `json:"token_env" msgpack:"token_env"`
	Dir      string `json:"dir,omitempty" msgpack:"dir,omitempty"` // where the session works, relative to the workspace
}

// Skill is a skill as a session receives it.
type Skill struct {
	Name        string            `json:"name" msgpack:"name"`
	Description string            `json:"description" msgpack:"description"`
	Body        string            `json:"body" msgpack:"body"`                       // SKILL.md body
	Files       map[string]string `json:"files,omitempty" msgpack:"files,omitempty"` // relative to the skill
}

// MCPServer is an MCP server the session may use, authenticated with the
// run token.
type MCPServer struct {
	Name string `json:"name" msgpack:"name"`
	URL  string `json:"url" msgpack:"url"`
}

// Start asks a Runner to execute a run.
type Start struct {
	Run  string `json:"run" msgpack:"run"`
	Spec Spec   `json:"spec" msgpack:"spec"`
}

// Cancel asks a Runner to stop a run.
type Cancel struct {
	Run string `json:"run" msgpack:"run"`
}

// Status reports that a run's session is running.
type Status struct {
	Run    string `json:"run" msgpack:"run"`
	Status string `json:"status" msgpack:"status"` // "running"
}

// Streams of run output.
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
	StreamSystem = "system" // the agent's own messages
	// StreamEvent carries a session's normalized events, one JSON Event
	// per line.
	StreamEvent = "event"
)

// Event kinds.
const (
	EventText       = "text"        // the agent's text
	EventToolUse    = "tool_use"    // the agent calls a tool: Tool, Input
	EventToolResult = "tool_result" // what the tool returned: Text, Error
	EventResult     = "result"      // a turn ended: Text is the final message, Error a failure
)

// Event is one normalized event of a coding-agent session.
type Event struct {
	Kind  string `json:"kind"`
	Text  string `json:"text,omitempty"`
	Tool  string `json:"tool,omitempty"`
	Input string `json:"input,omitempty"` // tool input, JSON
	Error bool   `json:"error,omitempty"`
}

// Result is what a coding-agent session reported at its end.
type Result struct {
	Success bool    `json:"success" msgpack:"success"`
	Summary string  `json:"summary" msgpack:"summary"` // the agent's final message
	Turns   int     `json:"turns" msgpack:"turns"`
	CostUSD float64 `json:"cost_usd" msgpack:"cost_usd"`
}

// Log is a chunk of run output. It is sent as a request: the Runner
// awaits it, so output reaches Core before run.finished.
type Log struct {
	Run    string `json:"run" msgpack:"run"`
	Stream string `json:"stream" msgpack:"stream"`
	Text   string `json:"text" msgpack:"text"`
}

// Finished reports a run's end. Error is set when the session could not
// run or was cancelled; otherwise ExitCode is the session's exit code.
type Finished struct {
	Run       string `json:"run" msgpack:"run"`
	ExitCode  int    `json:"exit_code" msgpack:"exit_code"`
	Error     string `json:"error,omitempty" msgpack:"error,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty" msgpack:"cancelled,omitempty"`
	// Result is the session's result (sessions only; nil when it reported
	// none).
	Result *Result `json:"result,omitempty" msgpack:"result,omitempty"`
}
