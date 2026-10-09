package app

import (
	"context"
	"fmt"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// Budget limits the tokens unattended work uses in a scope
// ("organization:<id>" or "project:<id>"); 0 means no limit. A project's
// ticket limit overrides its organization's.
type Budget struct {
	Scope        string
	TicketTokens int64 // per ticket, over its lifetime
	DailyTokens  int64 // per UTC day
	UpdatedBy    string
	UpdatedAt    time.Time
	Version      int64
}

// UsageFilter selects usage records; empty fields match everything.
type UsageFilter struct {
	OrganizationID, ProjectID, Ticket string
	Since, Until                      time.Time // Until zero: no bound
}

// BudgetStore persists budgets and sums usage.
type BudgetStore interface {
	// Budget returns a scope's budget; version 0 when none is set.
	Budget(ctx context.Context, scope string) (Budget, error)
	SetBudget(ctx context.Context, b Budget, expectedVersion int64, e event.Event) error
	// CountedTokens sums input, output and cache-write tokens.
	CountedTokens(ctx context.Context, f UsageFilter) (int64, error)
}

// Budgets bounds the spending of unattended work: per ticket, per project
// per day and per organization per day, in tokens reported by the gateway.
type Budgets struct {
	Store   BudgetStore
	Tenancy TenancyStore
	Authz   Authorizer
	Now     func() time.Time
}

// BudgetStatus is a budget with what its scope used today.
type BudgetStatus struct {
	Budget
	UsedToday int64
}

// Exceeded says which budget work ran out of.
type Exceeded struct {
	Scope       string // "ticket", "project" or "organization"
	Used, Limit int64
}

// Reason describes the exhausted budget for humans.
func (e Exceeded) Reason() string {
	per := map[string]string{"ticket": "budget of the ticket", "project": "daily budget of the project",
		"organization": "daily budget of the organization"}[e.Scope]
	return fmt.Sprintf("The %s is used up: %d of %d tokens.", per, e.Used, e.Limit)
}

// Get returns the budget of a project (projectKey) or, with "", of a
// organization, with today's use. Needs tracker.read on the project or
// organization.read on the organization.
func (bs *Budgets) Get(ctx context.Context, organizationKey, projectKey string) (BudgetStatus, error) {
	scope, filter, _, _, err := bs.scope(ctx, organizationKey, projectKey, ActTrackerRead, ActOrganizationRead)
	if err != nil {
		return BudgetStatus{}, err
	}
	b, err := bs.Store.Budget(ctx, scope)
	if err != nil {
		return BudgetStatus{}, err
	}
	filter.Since = bs.today()
	used, err := bs.Store.CountedTokens(ctx, filter)
	return BudgetStatus{Budget: b, UsedToday: used}, err
}

// Set sets the budget of a project or, with projectKey "", of an organization,
// if it is at version (0: never set). Needs project.update or
// organization.update.
func (bs *Budgets) Set(ctx context.Context, organizationKey, projectKey string, ticketTokens, dailyTokens, version int64) (BudgetStatus, error) {
	scope, _, p, c, err := bs.scope(ctx, organizationKey, projectKey, ActProjectUpdate, ActOrganizationUpdate)
	if err != nil {
		return BudgetStatus{}, err
	}
	if ticketTokens < 0 || dailyTokens < 0 {
		return BudgetStatus{}, fmt.Errorf("%w: budgets must not be negative (0: no limit)", ErrInvalid)
	}
	id, _ := caller(ctx)
	b := Budget{Scope: scope, TicketTokens: ticketTokens, DailyTokens: dailyTokens, UpdatedBy: id.Subject,
		UpdatedAt: bs.Now(), Version: version + 1}
	e := event.Event{Organization: c.ID, Project: p.ID, EntityType: "budget", EntityID: scope, Type: "budget.set",
		Actor: actorIn(ctx, id), OccurredAt: b.UpdatedAt,
		Payload: mustJSON(map[string]any{"ticket_tokens": ticketTokens, "daily_tokens": dailyTokens})}
	if err := bs.Store.SetBudget(ctx, b, version, e); err != nil {
		return BudgetStatus{}, err
	}
	return bs.Get(ctx, organizationKey, projectKey)
}

func (bs *Budgets) scope(ctx context.Context, organizationKey, projectKey string, projectAct, organizationAct Action) (
	string, UsageFilter, tenancy.Project, tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return "", UsageFilter{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	if projectKey != "" {
		p, err := bs.Tenancy.ProjectByKey(ctx, projectKey)
		if err != nil {
			return "", UsageFilter{}, tenancy.Project{}, tenancy.Organization{}, err
		}
		c, err := bs.Tenancy.OrganizationByID(ctx, p.OrganizationID)
		if err != nil {
			return "", UsageFilter{}, tenancy.Project{}, tenancy.Organization{}, err
		}
		if err := bs.Authz.Authorize(ctx, id, projectAct, Scope{Organization: c.Key, Project: p.Key}); err != nil {
			return "", UsageFilter{}, tenancy.Project{}, tenancy.Organization{}, err
		}
		return "project:" + p.ID, UsageFilter{ProjectID: p.ID}, p, c, nil
	}
	c, err := bs.Tenancy.OrganizationByKey(ctx, organizationKey)
	if err != nil {
		return "", UsageFilter{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	if err := bs.Authz.Authorize(ctx, id, organizationAct, Scope{Organization: c.Key}); err != nil {
		return "", UsageFilter{}, tenancy.Project{}, tenancy.Organization{}, err
	}
	return "organization:" + c.ID, UsageFilter{OrganizationID: c.ID}, tenancy.Project{}, c, nil
}

// Check returns the budget work in a project (and on a ticket, when
// ticketKey is set) ran out of, or nil.
func (bs *Budgets) Check(ctx context.Context, organizationID, projectID, ticketKey string) (*Exceeded, error) {
	pb, err := bs.Store.Budget(ctx, "project:"+projectID)
	if err != nil {
		return nil, err
	}
	cb, err := bs.Store.Budget(ctx, "organization:"+organizationID)
	if err != nil {
		return nil, err
	}
	ticketLimit := pb.TicketTokens
	if ticketLimit == 0 {
		ticketLimit = cb.TicketTokens
	}
	checks := []struct {
		scope  string
		limit  int64
		filter UsageFilter
	}{
		{"ticket", ticketLimit, UsageFilter{Ticket: ticketKey}},
		{"project", pb.DailyTokens, UsageFilter{ProjectID: projectID, Since: bs.today()}},
		{"organization", cb.DailyTokens, UsageFilter{OrganizationID: organizationID, Since: bs.today()}},
	}
	for _, ch := range checks {
		if ch.limit <= 0 || (ch.scope == "ticket" && ticketKey == "") {
			continue
		}
		used, err := bs.Store.CountedTokens(ctx, ch.filter)
		if err != nil {
			return nil, err
		}
		if used >= ch.limit {
			return &Exceeded{Scope: ch.scope, Used: used, Limit: ch.limit}, nil
		}
	}
	return nil, nil
}

// CheckTicket is Check for a ticket.
func (bs *Budgets) CheckTicket(ctx context.Context, it tracker.Item) (*Exceeded, error) {
	p, err := bs.Tenancy.ProjectByID(ctx, it.ProjectID)
	if err != nil {
		return nil, err
	}
	return bs.Check(ctx, p.OrganizationID, p.ID, it.Key)
}

// CheckKeys is Check by organization and project keys (the gateway's view).
func (bs *Budgets) CheckKeys(ctx context.Context, organizationKey, projectKey, ticketKey string) (*Exceeded, error) {
	c, err := bs.Tenancy.OrganizationByKey(ctx, organizationKey)
	if err != nil {
		return nil, err
	}
	p, err := bs.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return nil, err
	}
	if p.OrganizationID != c.ID {
		return nil, fmt.Errorf("%w: project %s is not a project of %s", ErrNotFound, projectKey, organizationKey)
	}
	return bs.Check(ctx, c.ID, p.ID, ticketKey)
}

func (bs *Budgets) today() time.Time {
	return bs.Now().UTC().Truncate(24 * time.Hour)
}
