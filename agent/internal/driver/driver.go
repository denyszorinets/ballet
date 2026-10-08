// Package driver describes how the agent drives a coding-agent runtime
// (ADR-0025): a driver turns a runtime-neutral session into files, an
// environment and a command; a codec, one per session, talks to the
// session's process over standard input and output.
package driver

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/denyszorinets/ballet/kit/runnerproto"
)

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
	// SessionID is the runtime's ID of the session, when the line told it.
	SessionID string
	// Reply is written to the session's standard input (protocol
	// handshakes, permission answers).
	Reply []byte
}

// Driver drives one runtime.
type Driver interface {
	Setup(s runnerproto.Session) (Setup, error)
	// NewCodec returns the codec of a new session working in dir (absolute).
	NewCodec(dir string) Codec
	// State reads what continues a session later (its transcript) from
	// the session's HOME; Setup restores it for a Session.Resume, whose
	// State is then uncompressed. Runtimes that cannot resume return an
	// error: the stage then starts a new session.
	State(home, sessionID string) ([]byte, error)
}

// Codec encodes input for one session's standard input and decodes its
// standard output, one line at a time.
type Codec interface {
	// Start encodes the start of the session with its prompt.
	Start(prompt string) []byte
	// Message encodes a user message for the next turn.
	Message(text string) []byte
	// Interrupt encodes a request to stop the current turn, which then
	// ends with a result; nil when the runtime cannot be interrupted.
	Interrupt() []byte
	Parse(line []byte) Parsed
}

var skillNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// SkillFiles lays out skills as <dir>/<name>/SKILL.md with their
// supporting files, refusing names and paths that escape their skill.
func SkillFiles(dir string, skills []runnerproto.Skill) (map[string]string, error) {
	files := map[string]string{}
	for _, sk := range skills {
		if !skillNameRe.MatchString(sk.Name) || strings.Contains(sk.Name, "..") {
			return nil, fmt.Errorf("invalid skill name %q", sk.Name)
		}
		d := dir + "/" + sk.Name
		files[d+"/SKILL.md"] = fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", sk.Name,
			strings.Join(strings.Fields(sk.Description), " "), sk.Body)
		for p, content := range sk.Files {
			clean := path.Clean(p)
			if path.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, "..") {
				return nil, fmt.Errorf("skill %s file %q escapes the skill", sk.Name, p)
			}
			files[d+"/"+clean] = content
		}
	}
	return files, nil
}
