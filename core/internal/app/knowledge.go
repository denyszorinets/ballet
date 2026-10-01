package app

import (
	"context"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// KnowledgeGrant is what Core lets a human do in a customer's knowledge
// space for one forwarded request (ADR-0022).
type KnowledgeGrant struct {
	Customer     string // customer key
	ActingFor    string // the human's subject
	Capabilities []string
}

// KnowledgeAccess authorizes humans' knowledge requests.
type KnowledgeAccess struct {
	Tenancy TenancyStore
	Authz   Authorizer
}

// Authorize checks knowledge.read (and knowledge.write when write is
// true) at the customer and returns the grant to put in the forwarded
// token: only the capabilities the request needs.
func (k *KnowledgeAccess) Authorize(ctx context.Context, customerKey string, write bool) (KnowledgeGrant, error) {
	id, err := caller(ctx)
	if err != nil {
		return KnowledgeGrant{}, err
	}
	c, err := k.Tenancy.CustomerByKey(ctx, customerKey)
	if err != nil {
		return KnowledgeGrant{}, err
	}
	action, caps := ActKnowledgeRead, []string{runtoken.CapKnowledgeRead}
	if write {
		action, caps = ActKnowledgeWrite, []string{runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite}
	}
	if err := k.Authz.Authorize(ctx, id, action, Scope{Customer: c.Key}); err != nil {
		return KnowledgeGrant{}, err
	}
	return KnowledgeGrant{Customer: c.Key, ActingFor: id.Subject, Capabilities: caps}, nil
}
