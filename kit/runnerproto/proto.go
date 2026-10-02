// Package runnerproto is the protocol between Core and Runners (ADR-0009):
// JSON-RPC over WebSocket (kit/rpc) at Path. The Runner connects with its
// runner token, introduces itself with runner.hello, and then receives
// run.start and run.cancel requests; it reports run.status, run.log and
// run.finished.
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
	SecretEnv      map[string]string `json:"secret_env,omitempty" msgpack:"secret_env,omitempty"`
	Workdir        string            `json:"workdir,omitempty" msgpack:"workdir,omitempty"`                 // relative to the run's workspace
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" msgpack:"timeout_seconds,omitempty"` // 0: Runner default
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
	StreamSystem = "system" // the Runner's own messages (pulling image, ...)
)

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
}
