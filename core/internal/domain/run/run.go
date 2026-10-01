// Package run models runs: one agent session executing one pipeline stage
// of one ticket on a Runner (ADR-0009).
package run

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Status of a run.
type Status string

// Statuses. A run is queued until a Runner takes it (starting), runs, and
// ends succeeded, failed or cancelled.
const (
	StatusQueued    Status = "queued"
	StatusStarting  Status = "starting"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Terminal reports whether s is final.
func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// Active reports whether a Runner holds a run in status s.
func (s Status) Active() bool { return s == StatusStarting || s == StatusRunning }

var transitions = map[Status][]Status{
	StatusQueued: {StatusStarting, StatusCancelled},
	// A Runner that refuses a run sends it back to the queue.
	StatusStarting: {StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, StatusQueued},
	StatusRunning:  {StatusSucceeded, StatusFailed, StatusCancelled},
}

// CanTransition reports whether a run may move from one status to another.
func CanTransition(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Spec is what the Runner executes.
type Spec struct {
	Image          string            `json:"image,omitempty"`
	Command        []string          `json:"command"`
	Env            map[string]string `json:"env,omitempty"`
	Workdir        string            `json:"workdir,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// Run is one session.
type Run struct {
	ID         string
	ProjectID  string
	TicketID   string
	Stage      string // pipeline stage, e.g. "implement"
	Status     Status
	Spec       Spec
	Runner     string // the Runner executing it
	ExitCode   *int
	Error      string // why it failed or was cancelled
	CreatedBy  string // subject of who queued it
	CreatedAt  time.Time
	StartedAt  time.Time // zero until running
	FinishedAt time.Time // zero until terminal
	Version    int64
}

var (
	stageRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Validate checks a new run.
func (r Run) Validate() error {
	var errs []error
	if !stageRe.MatchString(r.Stage) {
		errs = append(errs, fmt.Errorf("stage %q must be 1-32 lowercase letters, digits, - or _", r.Stage))
	}
	if len(r.Spec.Command) == 0 || r.Spec.Command[0] == "" {
		errs = append(errs, errors.New("a run needs a command"))
	}
	for k := range r.Spec.Env {
		if !envKeyRe.MatchString(k) {
			errs = append(errs, fmt.Errorf("invalid environment variable name %q", k))
		}
	}
	if r.Spec.TimeoutSeconds < 0 {
		errs = append(errs, errors.New("timeout must not be negative"))
	}
	return errors.Join(errs...)
}
