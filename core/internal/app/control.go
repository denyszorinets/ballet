package app

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

// PausePlatform is the scope of a pause of all autonomous work.
const PausePlatform = "platform"

// Pause stops autonomous work in a scope: PausePlatform or a project ID.
type Pause struct {
	Scope    string
	Reason   string
	PausedBy string
	PausedAt time.Time
}

// ControlStore persists pauses.
type ControlStore interface {
	SetPause(ctx context.Context, p Pause, e event.Event) error
	// ClearPause removes a pause (ErrNotFound when there is none).
	ClearPause(ctx context.Context, scope string, e event.Event) error
	Pauses(ctx context.Context) ([]Pause, error)
}

// Control lets humans stop autonomous work at once: pausing a project or
// the platform stops new stages (the scheduler starts no tickets, the
// dispatcher holds queued runs, flows wait before their next stage); the
// kill switch also cancels the runs in progress. Resuming continues the
// waiting flows; a stage whose run was cancelled runs again.
type Control struct {
	Store      ControlStore
	Tenancy    TenancyStore
	Authz      Authorizer
	RunStore   RunStore
	Dispatcher *Dispatcher
	Flows      *Flows // resumed flows continue; optional
	Now        func() time.Time
	Logger     *slog.Logger
}

// PauseView is a pause with its project's key ("" for the platform).
type PauseView struct {
	Pause
	ProjectKey string
}

// Paused reports whether autonomous work in a project is paused. It fails
// safe: when pauses cannot be read, work counts as paused.
func (ct *Control) Paused(ctx context.Context, projectID string) bool {
	pauses, err := ct.Store.Pauses(ctx)
	if err != nil {
		ct.logger().WarnContext(ctx, "reading pauses failed; holding work", "error", err)
		return true
	}
	return slices.ContainsFunc(pauses, func(p Pause) bool { return p.Scope == PausePlatform || p.Scope == projectID })
}

// List returns the pauses the caller can see.
func (ct *Control) List(ctx context.Context) ([]PauseView, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	pauses, err := ct.Store.Pauses(ctx)
	if err != nil {
		return nil, err
	}
	out := []PauseView{}
	for _, p := range pauses {
		if p.Scope == PausePlatform {
			out = append(out, PauseView{Pause: p})
			continue
		}
		pr, c, err := ct.project(ctx, p.Scope)
		if err != nil {
			continue // the project is gone
		}
		if ct.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Organization: c.Key, Project: pr.Key}) == nil {
			out = append(out, PauseView{Pause: p, ProjectKey: pr.Key})
		}
	}
	return out, nil
}

// Pause pauses a project (projectKey) or, with "", the platform.
// Needs run.manage on the project, or at platform scope.
func (ct *Control) Pause(ctx context.Context, projectKey, reason string) (PauseView, error) {
	return ct.pause(ctx, projectKey, reason, false)
}

// Kill pauses like Pause and cancels every run in progress in the scope.
// It returns the pause and how many runs it cancelled.
func (ct *Control) Kill(ctx context.Context, projectKey, reason string) (PauseView, int, error) {
	v, err := ct.pause(ctx, projectKey, reason, true)
	if err != nil {
		return PauseView{}, 0, err
	}
	runs, err := ct.RunStore.ListRuns(ctx, RunFilter{Statuses: []run.Status{run.StatusQueued, run.StatusStarting, run.StatusRunning}})
	if err != nil {
		return v, 0, err
	}
	n := 0
	for _, r := range runs {
		if v.Scope != PausePlatform && r.ProjectID != v.Scope {
			continue
		}
		if _, err := ct.Dispatcher.cancel(ctx, r, event.Actor{Kind: event.ActorHuman, Subject: v.PausedBy}); err != nil {
			ct.logger().WarnContext(ctx, "kill switch: cancelling a run failed", "run", r.ID, "error", err)
			continue
		}
		n++
	}
	return v, n, nil
}

func (ct *Control) pause(ctx context.Context, projectKey, reason string, kill bool) (PauseView, error) {
	id, scope, p, c, err := ct.authorize(ctx, projectKey)
	if err != nil {
		return PauseView{}, err
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 1000 {
		return PauseView{}, fmt.Errorf("%w: reason must be at most 1000 characters", ErrInvalid)
	}
	pause := Pause{Scope: scope, Reason: reason, PausedBy: id.Subject, PausedAt: ct.Now()}
	typ := "control.paused"
	if kill {
		typ = "control.killed"
	}
	if err := ct.Store.SetPause(ctx, pause, ct.event(p, c, typ, actorIn(ctx, id), reason)); err != nil {
		return PauseView{}, err
	}
	return PauseView{Pause: pause, ProjectKey: p.Key}, nil
}

// Resume ends a pause of a project (projectKey) or of the platform
// (""); the flows waiting for it continue.
func (ct *Control) Resume(ctx context.Context, projectKey string) error {
	id, scope, p, c, err := ct.authorize(ctx, projectKey)
	if err != nil {
		return err
	}
	if err := ct.Store.ClearPause(ctx, scope, ct.event(p, c, "control.resumed", actorIn(ctx, id), "")); err != nil {
		return err
	}
	if ct.Flows != nil {
		ct.Flows.ResumePaused(ctx)
	}
	if ct.Dispatcher != nil {
		ct.Dispatcher.Kick()
	}
	return nil
}

func (ct *Control) authorize(ctx context.Context, projectKey string) (identity, string, tenancy.Project, tenancy.Organization, error) {
	id, err := caller(ctx)
	if err != nil {
		return identity{}, "", tenancy.Project{}, tenancy.Organization{}, err
	}
	if projectKey == "" {
		if err := ct.Authz.Authorize(ctx, id, ActRunManage, Scope{}); err != nil {
			return identity{}, "", tenancy.Project{}, tenancy.Organization{}, err
		}
		return id, PausePlatform, tenancy.Project{}, tenancy.Organization{}, nil
	}
	p, err := ct.Tenancy.ProjectByKey(ctx, projectKey)
	if err != nil {
		return identity{}, "", tenancy.Project{}, tenancy.Organization{}, err
	}
	c, err := ct.Tenancy.OrganizationByID(ctx, p.OrganizationID)
	if err != nil {
		return identity{}, "", tenancy.Project{}, tenancy.Organization{}, err
	}
	if err := ct.Authz.Authorize(ctx, id, ActRunManage, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return identity{}, "", tenancy.Project{}, tenancy.Organization{}, err
	}
	return id, p.ID, p, c, nil
}

func (ct *Control) project(ctx context.Context, projectID string) (tenancy.Project, tenancy.Organization, error) {
	p, err := ct.Tenancy.ProjectByID(ctx, projectID)
	if err != nil {
		return tenancy.Project{}, tenancy.Organization{}, err
	}
	c, err := ct.Tenancy.OrganizationByID(ctx, p.OrganizationID)
	return p, c, err
}

func (ct *Control) event(p tenancy.Project, c tenancy.Organization, typ string, actor event.Actor, reason string) event.Event {
	e := event.Event{Organization: c.ID, Project: p.ID, EntityType: "project", EntityID: p.ID, Type: typ, Actor: actor,
		OccurredAt: ct.Now(), Payload: mustJSON(map[string]any{"reason": reason})}
	if p.ID == "" {
		e.EntityType, e.EntityID = "platform", PausePlatform
	}
	return e
}

func (ct *Control) logger() *slog.Logger {
	if ct.Logger != nil {
		return ct.Logger
	}
	return slog.Default()
}
