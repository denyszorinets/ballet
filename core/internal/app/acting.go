package app

import (
	"context"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
)

type plannerSessionKey struct{}

// ActingAsPlanner marks ctx as the planner of a session acting for the
// caller (the human in ctx). Use cases authorize as the human, record the
// planner as the actor, and refuse what only humans may do (approving).
func ActingAsPlanner(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, plannerSessionKey{}, sessionID)
}

// PlannerSessionOf returns the planner session ctx acts as, if any.
func PlannerSessionOf(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(plannerSessionKey{}).(string)
	return s, ok && s != ""
}

// actorIn is the event actor for the caller in ctx: the planner acting for
// the human when ctx is marked, the caller otherwise.
func actorIn(ctx context.Context, id identity) event.Actor {
	if s, ok := PlannerSessionOf(ctx); ok {
		return event.Actor{Kind: event.ActorService, Subject: "planner:" + s, ActingFor: id.Subject}
	}
	return actorOf(id)
}
