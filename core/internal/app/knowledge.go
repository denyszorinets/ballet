package app

import (
	"context"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// KnowledgeGrant is what Core lets a human do in an organization's knowledge
// space for one forwarded request (ADR-0022).
type KnowledgeGrant struct {
	Organization string // organization key
	ActingFor    string // the human's subject
	Capabilities []string
}

// KnowledgeAccess authorizes humans' knowledge requests.
type KnowledgeAccess struct {
	Tenancy TenancyStore
	Authz   Authorizer
}

// Authorize checks knowledge.read (and knowledge.write when write is
// true) at the organization and returns the grant to put in the forwarded
// token: only the capabilities the request needs.
func (k *KnowledgeAccess) Authorize(ctx context.Context, organizationKey string, write bool) (KnowledgeGrant, error) {
	id, err := caller(ctx)
	if err != nil {
		return KnowledgeGrant{}, err
	}
	c, err := k.Tenancy.OrganizationByKey(ctx, organizationKey)
	if err != nil {
		return KnowledgeGrant{}, err
	}
	action, caps := ActKnowledgeRead, []string{runtoken.CapKnowledgeRead}
	if write {
		action, caps = ActKnowledgeWrite, []string{runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite}
	}
	if err := k.Authz.Authorize(ctx, id, action, Scope{Organization: c.Key}); err != nil {
		return KnowledgeGrant{}, err
	}
	return KnowledgeGrant{Organization: c.Key, ActingFor: id.Subject, Capabilities: caps}, nil
}
