package app

import (
	"context"
	"fmt"
	"time"
)

// UsageRecord is the LLM usage of one request (reported by the gateway).
type UsageRecord struct {
	OccurredAt   time.Time
	CustomerID   string
	ProjectID    string
	Ticket       string // ticket key
	Run          string
	Model        string
	Status       int
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	CacheWrite   int64
}

// UsageTotals aggregates usage.
type UsageTotals struct {
	Key          string // ticket key or model; "" for the grand total
	Requests     int64
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	CacheWrite   int64
}

// UsageStore persists and aggregates usage.
type UsageStore interface {
	InsertUsage(ctx context.Context, records []UsageRecord) error
	AggregateUsage(ctx context.Context, projectID, groupBy string, since time.Time) ([]UsageTotals, error)
}

// Usage ingests and reports LLM usage.
type Usage struct {
	Store   UsageStore
	Tenancy TenancyStore
	Authz   Authorizer
	// Observe, when set, sees every stored record (metrics).
	Observe func(r UsageInput)
}

// UsageInput is a record as reported by the gateway, with customer and
// project keys.
type UsageInput struct {
	OccurredAt                                       time.Time
	Customer, Project, Ticket, Run, Model            string
	Status                                           int
	InputTokens, OutputTokens, CacheRead, CacheWrite int64
}

// Ingest stores records from the gateway (callers hold usage.write).
// Records of unknown or mismatched customer/project are skipped and
// counted.
func (u *Usage) Ingest(ctx context.Context, in []UsageInput) (stored, skipped int, err error) {
	resolved := map[string]*usageScope{}
	var out []UsageRecord
	var kept []UsageInput
	for _, r := range in {
		key := r.Customer + "/" + r.Project
		id, ok := resolved[key]
		if !ok {
			id = u.resolve(ctx, r.Customer, r.Project)
			resolved[key] = id
		}
		if id == nil {
			skipped++
			continue
		}
		kept = append(kept, r)
		out = append(out, UsageRecord{
			OccurredAt: r.OccurredAt, CustomerID: id.customer, ProjectID: id.project, Ticket: r.Ticket, Run: r.Run,
			Model: r.Model, Status: r.Status, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			CacheRead: r.CacheRead, CacheWrite: r.CacheWrite,
		})
	}
	if len(out) > 0 {
		if err := u.Store.InsertUsage(ctx, out); err != nil {
			return 0, 0, err
		}
	}
	if u.Observe != nil {
		for _, r := range kept {
			u.Observe(r)
		}
	}
	return len(out), skipped, nil
}

// usageScope holds the IDs a usage record is attributed to.
type usageScope struct{ customer, project string }

func (u *Usage) resolve(ctx context.Context, customerKey, projectKey string) *usageScope {
	p, err := u.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return nil
	}
	c, err := u.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil || c.Key != customerKey {
		return nil
	}
	return &usageScope{customer: c.ID, project: p.ID}
}

// UsageReport is a project's usage, grouped.
type UsageReport struct {
	Groups []UsageTotals
	Total  UsageTotals
}

// Report aggregates a project's usage since a time, grouped by "ticket"
// or "model". Requires tracker.read on the project.
func (u *Usage) Report(ctx context.Context, projectKey, groupBy string, since time.Time) (UsageReport, error) {
	if groupBy != "ticket" && groupBy != "model" {
		return UsageReport{}, fmt.Errorf("%w: group_by must be ticket or model", ErrInvalid)
	}
	id, err := caller(ctx)
	if err != nil {
		return UsageReport{}, err
	}
	p, err := u.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return UsageReport{}, err
	}
	c, err := u.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return UsageReport{}, err
	}
	if err := u.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return UsageReport{}, err
	}
	groups, err := u.Store.AggregateUsage(ctx, p.ID, groupBy, since)
	if err != nil {
		return UsageReport{}, err
	}
	rep := UsageReport{Groups: groups}
	for _, g := range groups {
		rep.Total.Requests += g.Requests
		rep.Total.InputTokens += g.InputTokens
		rep.Total.OutputTokens += g.OutputTokens
		rep.Total.CacheRead += g.CacheRead
		rep.Total.CacheWrite += g.CacheWrite
	}
	return rep, nil
}
