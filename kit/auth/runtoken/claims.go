// Package runtoken issues and verifies the short-lived tokens Core gives to
// workloads — agent runs, Runners, planner sessions and services (ADR-0006).
//
// Tokens are JWTs signed by Core with Ed25519 keys from a KeyRing. Other
// services verify them against Core's public JWKS (/.well-known/jwks.json).
package runtoken

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Issuer is the "iss" of every run token.
const Issuer = "ballet-core"

// Kind is the kind of workload a token identifies.
type Kind string

// Token kinds.
const (
	KindRun     Kind = "run"     // one agent session executing a ticket stage
	KindRunner  Kind = "runner"  // a Runner process
	KindPlanner Kind = "planner" // a planner session acting for a human
	KindService Kind = "service" // a Ballet service calling another
)

// Capabilities a token can grant. Services check them per operation.
const (
	CapTrackerRead    = "tracker.read"    // read own ticket and plan context
	CapTrackerReport  = "tracker.report"  // report progress, stage reports, questions, proposals
	CapKnowledgeRead  = "knowledge.read"  // search and read the customer's knowledge
	CapKnowledgeWrite = "knowledge.write" // create and update knowledge entries
	CapLLMInvoke      = "llm.invoke"      // call the LLM gateway
	CapRunnerConnect  = "runner.connect"  // connect a Runner to Core
)

var knownCapabilities = []string{
	CapTrackerRead, CapTrackerReport, CapKnowledgeRead, CapKnowledgeWrite, CapLLMInvoke, CapRunnerConnect,
}

// Claims are the contents of a run token.
type Claims struct {
	ID       string    // unique token id (jti); set on issue
	Kind     Kind      // workload kind
	Subject  string    // e.g. "run:<id>", "runner:<id>", "planner:<session>"
	Audience []string  // services that accept the token, e.g. "knowledge"
	Expiry   time.Time // set on issue

	Customer  string // customer scope; required for run and planner tokens
	Project   string // project scope; required for run and planner tokens
	Ticket    string // ticket; required for run tokens
	Session   string // planner session; required for planner tokens
	ActingFor string // subject of the human a planner acts for

	Capabilities []string
}

// Can reports whether the token grants capability.
func (c Claims) Can(capability string) bool {
	return slices.Contains(c.Capabilities, capability)
}

// Validate checks the claims needed for the token's kind.
func (c Claims) Validate() error {
	var errs []error
	switch c.Kind {
	case KindRun, KindRunner, KindPlanner, KindService:
	default:
		errs = append(errs, fmt.Errorf("unknown token kind %q", c.Kind))
	}
	if c.Subject == "" {
		errs = append(errs, errors.New("subject is required"))
	}
	if len(c.Audience) == 0 {
		errs = append(errs, errors.New("audience is required"))
	}
	if c.Kind == KindRun || c.Kind == KindPlanner {
		if c.Customer == "" || c.Project == "" {
			errs = append(errs, fmt.Errorf("%s tokens need customer and project", c.Kind))
		}
	}
	if c.Kind == KindRun && c.Ticket == "" {
		errs = append(errs, errors.New("run tokens need a ticket"))
	}
	if c.Kind == KindPlanner && (c.Session == "" || c.ActingFor == "") {
		errs = append(errs, errors.New("planner tokens need a session and the human they act for"))
	}
	for _, capability := range c.Capabilities {
		if !slices.Contains(knownCapabilities, capability) {
			errs = append(errs, fmt.Errorf("unknown capability %q", capability))
		}
	}
	return errors.Join(errs...)
}
