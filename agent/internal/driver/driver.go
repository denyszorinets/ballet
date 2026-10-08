// Package driver describes how the agent drives a coding-agent runtime
// (ADR-0025): a driver turns a runtime-neutral session into files, an
// environment and a command, encodes messages for the session's standard
// input, and normalizes what the session prints.
package driver

import "github.com/denyszorinets/ballet/kit/runnerproto"

// Setup is what a session process needs.
type Setup struct {
	Files   map[string]string // relative to the session's HOME
	Env     map[string]string // values may reference the session's environment as ${VAR}
	Command []string
}

// Parsed is what one line of a session's standard output said.
type Parsed struct {
	Events []runnerproto.Event
	// Result is set when a turn ended: the session waits for the next
	// message or, when none comes, for its standard input to close.
	Result *runnerproto.Result
}

// Driver drives one runtime.
type Driver interface {
	Setup(s runnerproto.Session) (Setup, error)
	// Message encodes a user message as a line for standard input.
	Message(text string) []byte
	// Interrupt encodes a request to stop the current turn, which then
	// ends with a result; nil when the runtime cannot be interrupted.
	Interrupt() []byte
	Parse(line []byte) Parsed
}
