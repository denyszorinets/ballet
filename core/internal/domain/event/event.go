// Package event defines Core's audit/event record: every change to a
// tracked entity produces exactly one Event, written atomically with the
// change. Events feed history views, realtime streams and the digest.
package event

import (
	"encoding/json"
	"time"
)

// ActorKind tells who caused an event.
type ActorKind string

// Actor kinds.
const (
	ActorHuman   ActorKind = "human"
	ActorService ActorKind = "service"
	ActorSystem  ActorKind = "system" // Core itself (scheduler, orchestrator)
)

// Actor is who caused an event.
type Actor struct {
	Kind      ActorKind `json:"kind"`
	Subject   string    `json:"subject"`
	ActingFor string    `json:"acting_for,omitempty"` // human behind a planner
}

// System is the actor for changes Core makes on its own.
var System = Actor{Kind: ActorSystem, Subject: "ballet-core"}

// Event is one recorded change.
type Event struct {
	Seq          int64           `json:"seq"` // monotonic, assigned by the store
	ID           string          `json:"id"`
	OccurredAt   time.Time       `json:"occurred_at"`
	Organization string          `json:"organization,omitempty"`
	Project      string          `json:"project,omitempty"`
	EntityType   string          `json:"entity_type"` // e.g. "ticket"
	EntityID     string          `json:"entity_id"`
	Type         string          `json:"type"` // e.g. "ticket.created"
	Actor        Actor           `json:"actor"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}
