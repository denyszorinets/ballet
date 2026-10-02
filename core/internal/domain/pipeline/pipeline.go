// Package pipeline defines ticket pipelines as data, per project
// (ADR-0017): stages of kind agent, human or platform, and transitions by
// outcome. Ballet hard-codes only the invariants around them.
package pipeline

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Kind of a stage.
type Kind string

// Stage kinds.
const (
	KindAgent    Kind = "agent"    // a coding agent session (separate per stage, ADR-0014)
	KindHuman    Kind = "human"    // a human approves (done) or rejects (failed)
	KindPlatform Kind = "platform" // Ballet acts on the forge
)

// Outcome of a stage.
type Outcome string

// Outcomes.
const (
	OutcomeDone    Outcome = "done"
	OutcomeFailed  Outcome = "failed"
	OutcomeBlocked Outcome = "blocked"
)

// Special transition targets.
const (
	TargetDone     = "$done"     // the pipeline completed
	TargetFailed   = "$failed"   // the pipeline failed
	TargetQuestion = "$question" // ask the planner and humans, then resume the stage
)

// Platform actions.
const (
	// ActionMerge opens the pull request if needed, waits for checks and the
	// approvals the ticket's policy requires, and merges when the policy
	// allows (auto) or a human does (manual).
	ActionMerge = "merge"
)

// Stage is one step of a pipeline.
type Stage struct {
	ID             string             `yaml:"id" json:"id"`
	Kind           Kind               `yaml:"kind" json:"kind"`
	Name           string             `yaml:"name,omitempty" json:"name,omitempty"`
	Instructions   string             `yaml:"instructions,omitempty" json:"instructions,omitempty"`       // agent
	Adapter        string             `yaml:"adapter,omitempty" json:"adapter,omitempty"`                 // agent; default claude-code
	Model          string             `yaml:"model,omitempty" json:"model,omitempty"`                     // agent
	Skills         []string           `yaml:"skills,omitempty" json:"skills,omitempty"`                   // agent: project skills to include; empty: all
	Action         string             `yaml:"action,omitempty" json:"action,omitempty"`                   // platform
	TimeoutMinutes int                `yaml:"timeout_minutes,omitempty" json:"timeout_minutes,omitempty"` // 0: default
	Next           map[Outcome]string `yaml:"next,omitempty" json:"next,omitempty"`                       // outcome → stage id or $target
}

// Definition is a pipeline.
type Definition struct {
	Stages        []Stage `yaml:"stages" json:"stages"`
	MaxIterations int     `yaml:"max_iterations" json:"max_iterations"` // loops back to earlier stages before asking
}

// DefaultAdapter runs agent stages that name none.
const DefaultAdapter = "claude-code"

var (
	idRe    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	nameRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	actions = []string{ActionMerge}
)

// ValidName checks a pipeline name ("default" or a ticket type).
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("pipeline name %q must be 1-32 lowercase letters, digits, - or _", name)
	}
	return nil
}

// Stage returns the stage with id.
func (d Definition) Stage(id string) (Stage, bool) {
	for _, s := range d.Stages {
		if s.ID == id {
			return s, true
		}
	}
	return Stage{}, false
}

// Target returns where outcome of stage leads: its transition, else the
// defaults (done → next stage or $done; failed and blocked → $question).
func (d Definition) Target(stageID string, outcome Outcome) string {
	for i, s := range d.Stages {
		if s.ID != stageID {
			continue
		}
		if t, ok := s.Next[outcome]; ok {
			return t
		}
		if outcome == OutcomeDone {
			if i+1 < len(d.Stages) {
				return d.Stages[i+1].ID
			}
			return TargetDone
		}
		return TargetQuestion
	}
	return TargetFailed
}

// IsLoop reports whether moving from stage from to stage to goes back in
// the pipeline (an iteration).
func (d Definition) IsLoop(from, to string) bool {
	fi, ti := -1, -1
	for i, s := range d.Stages {
		if s.ID == from {
			fi = i
		}
		if s.ID == to {
			ti = i
		}
	}
	return ti >= 0 && fi >= 0 && ti <= fi
}

// Validate checks the definition: stages, kinds, transitions, that every
// stage is reachable and that the pipeline can complete.
func (d Definition) Validate(adapters []string) error {
	var errs []error
	if len(d.Stages) == 0 || len(d.Stages) > 20 {
		errs = append(errs, errors.New("a pipeline needs 1-20 stages"))
	}
	if d.MaxIterations < 1 || d.MaxIterations > 20 {
		errs = append(errs, errors.New("max_iterations must be 1-20"))
	}
	seen := map[string]bool{}
	for _, s := range d.Stages {
		if !idRe.MatchString(s.ID) {
			errs = append(errs, fmt.Errorf("stage id %q must be 1-32 lowercase letters, digits, - or _", s.ID))
		}
		if seen[s.ID] {
			errs = append(errs, fmt.Errorf("stage %q is defined twice", s.ID))
		}
		seen[s.ID] = true
	}
	for _, s := range d.Stages {
		errs = append(errs, s.validate(seen, adapters)...)
	}
	if len(errs) == 0 {
		errs = append(errs, d.reachability()...)
	}
	return errors.Join(errs...)
}

func (s Stage) validate(ids map[string]bool, adapters []string) []error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("stage %s: "+format, append([]any{s.ID}, args...)...))
	}
	switch s.Kind {
	case KindAgent:
		a := s.Adapter
		if a == "" {
			a = DefaultAdapter
		}
		if adapters != nil && !slices.Contains(adapters, a) {
			fail("unknown adapter %q", a)
		}
		if s.Action != "" {
			fail("agent stages have no action")
		}
	case KindHuman:
		if s.Adapter != "" || s.Action != "" || len(s.Skills) > 0 {
			fail("human stages have no adapter, action or skills")
		}
	case KindPlatform:
		if !slices.Contains(actions, s.Action) {
			fail("action %q must be one of %s", s.Action, strings.Join(actions, ", "))
		}
		if s.Adapter != "" || len(s.Skills) > 0 {
			fail("platform stages have no adapter or skills")
		}
	default:
		fail("kind %q must be agent, human or platform", s.Kind)
	}
	if utf8.RuneCountInString(s.Instructions) > 20_000 || utf8.RuneCountInString(s.Name) > 100 {
		fail("name or instructions too long")
	}
	if s.TimeoutMinutes < 0 || s.TimeoutMinutes > 24*60 {
		fail("timeout_minutes must be 0-1440")
	}
	for o, t := range s.Next {
		if o != OutcomeDone && o != OutcomeFailed && o != OutcomeBlocked {
			fail("unknown outcome %q", o)
		}
		if t != TargetDone && t != TargetFailed && t != TargetQuestion && !ids[t] {
			fail("%s leads to unknown stage %q", o, t)
		}
	}
	return errs
}

// reachability checks that every stage can be reached from the first and
// that the first can reach $done.
func (d Definition) reachability() []error {
	reached := map[string]bool{d.Stages[0].ID: true}
	queue := []string{d.Stages[0].ID}
	done := false
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, o := range []Outcome{OutcomeDone, OutcomeFailed, OutcomeBlocked} {
			t := d.Target(id, o)
			switch {
			case t == TargetDone:
				done = true
			case strings.HasPrefix(t, "$"):
			case !reached[t]:
				reached[t] = true
				queue = append(queue, t)
			}
		}
	}
	var errs []error
	for _, s := range d.Stages {
		if !reached[s.ID] {
			errs = append(errs, fmt.Errorf("stage %s can never be reached", s.ID))
		}
	}
	if !done {
		errs = append(errs, errors.New("the pipeline can never complete ($done is unreachable)"))
	}
	return errs
}

// Default is Ballet's default pipeline: Implement → Review → Verify →
// Integrate, separate sessions, failures back to Implement.
func Default() Definition {
	return Definition{MaxIterations: 3, Stages: []Stage{
		{ID: "implement", Kind: KindAgent, Name: "Implement",
			Instructions: "Implement the ticket so that every acceptance criterion holds. Add or update tests and " +
				"documentation as this project's skills require. Commit in small steps with clear messages and push. " +
				"End with submit_stage_report.",
			Next: map[Outcome]string{OutcomeDone: "review", OutcomeFailed: TargetQuestion, OutcomeBlocked: TargetQuestion}},
		{ID: "review", Kind: KindAgent, Name: "Review",
			Instructions: "Review the changes on this branch against the ticket and its acceptance criteria: correctness, " +
				"tests, documentation, maintainability. Do not rewrite the implementation. Submit done when it is ready, " +
				"failed with concrete findings when it is not.",
			Next: map[Outcome]string{OutcomeDone: "verify", OutcomeFailed: "implement", OutcomeBlocked: TargetQuestion}},
		{ID: "verify", Kind: KindAgent, Name: "Verify",
			Instructions: "Verify that the acceptance criteria hold: build, run the tests and exercise the behaviour. " +
				"Submit done when they hold, failed with what does not.",
			Next: map[Outcome]string{OutcomeDone: "integrate", OutcomeFailed: "implement", OutcomeBlocked: TargetQuestion}},
		{ID: "integrate", Kind: KindPlatform, Name: "Integrate", Action: ActionMerge,
			Next: map[Outcome]string{OutcomeDone: TargetDone, OutcomeFailed: "implement", OutcomeBlocked: TargetQuestion}},
	}}
}

// YAML renders a definition as YAML.
func (d Definition) YAML() (string, error) {
	b, err := yaml.Marshal(d)
	return string(b), err
}

// ParseYAML reads a definition from YAML; unknown fields are errors.
func ParseYAML(src string) (Definition, error) {
	var d Definition
	dec := yaml.NewDecoder(strings.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return Definition{}, fmt.Errorf("pipeline YAML: %w", err)
	}
	return d, nil
}
