package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/credential"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/auth"
)

// TicketPR is a ticket's pull request.
type TicketPR struct {
	forge.PullRequest
	TicketID  string
	ProjectID string
	Forge     string
}

// PRStore persists tickets' pull requests.
type PRStore interface {
	SavePullRequest(ctx context.Context, p TicketPR, e *event.Event) error
	PullRequest(ctx context.Context, ticketID string) (TicketPR, error)
	OpenPullRequests(ctx context.Context) ([]TicketPR, error)
}

// PRView is a ticket's pull request with the ticket's key.
type PRView struct {
	TicketPR
	TicketKey string
}

// PullRequests links tickets to pull requests on their project's forge
// (ADR-0007) and follows them.
type PullRequests struct {
	Store     PRStore
	Execution ExecutionStore
	Items     ItemStore
	Tenancy   TenancyStore
	Authz     Authorizer
	// Forges builds the adapter for a project's settings.
	Forges func(s execution.Settings) (forge.Forge, error)
	// Token returns the project's git token ("" when none).
	Token  func(ctx context.Context, organizationKey, projectKey string) (string, error)
	Now    func() time.Time
	Logger *slog.Logger
}

type prScope struct {
	ticket       tracker.Item
	project      tenancy.Project
	organization tenancy.Organization
	settings     execution.Settings
	forge        forge.Forge
	repo         forge.Repo
}

func (ps *PullRequests) scope(ctx context.Context, ticketKey string, a Action) (prScope, identity, error) {
	id, err := caller(ctx)
	if err != nil {
		return prScope{}, identity{}, err
	}
	it, err := ps.Items.ItemByKey(ctx, ticketKey)
	if err != nil {
		return prScope{}, identity{}, err
	}
	s, err := ps.load(ctx, it)
	if err != nil {
		return prScope{}, identity{}, err
	}
	if err := ps.Authz.Authorize(ctx, id, a, Scope{Organization: s.organization.Key, Project: s.project.Key}); err != nil {
		return prScope{}, identity{}, err
	}
	return s, id, nil
}

// load resolves a ticket's project, forge and repository.
func (ps *PullRequests) load(ctx context.Context, it tracker.Item) (prScope, error) {
	p, err := ps.Tenancy.ProjectByID(ctx, it.ProjectID)
	if err != nil {
		return prScope{}, err
	}
	c, err := ps.Tenancy.OrganizationByID(ctx, p.OrganizationID)
	if err != nil {
		return prScope{}, err
	}
	s := prScope{ticket: it, project: p, organization: c}
	if s.settings, err = ps.Execution.ExecutionSettings(ctx, p.ID); errors.Is(err, ErrNotFound) || (err == nil && s.settings.RepoURL == "") {
		return s, fmt.Errorf("%w: project %s has no repository", ErrInvalid, p.Key)
	} else if err != nil {
		return s, err
	}
	if s.forge, err = ps.Forges(s.settings); err != nil {
		return s, invalid(err)
	}
	s.repo = forge.Repo{URL: s.settings.RepoURL}
	if _, owner, name, err := forge.ParseRepo(s.settings.RepoURL); err == nil {
		s.repo.Owner, s.repo.Name = owner, name
	}
	if s.repo.Token, err = ps.Token(ctx, c.Key, p.Key); err != nil {
		return s, err
	}
	return s, nil
}

// Get returns a ticket's pull request (tracker.read).
func (ps *PullRequests) Get(ctx context.Context, ticketKey string) (PRView, error) {
	id, err := caller(ctx)
	if err != nil {
		return PRView{}, err
	}
	it, err := ps.Items.ItemByKey(ctx, ticketKey)
	if err != nil {
		return PRView{}, err
	}
	p, err := ps.Tenancy.ProjectByID(ctx, it.ProjectID)
	if err != nil {
		return PRView{}, err
	}
	c, err := ps.Tenancy.OrganizationByID(ctx, p.OrganizationID)
	if err != nil {
		return PRView{}, err
	}
	if err := ps.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Organization: c.Key, Project: p.Key}); err != nil {
		return PRView{}, err
	}
	pr, err := ps.Store.PullRequest(ctx, it.ID)
	if err != nil {
		return PRView{}, err
	}
	return PRView{TicketPR: pr, TicketKey: it.Key}, nil
}

// Open opens (or finds) the pull request of the ticket's branch into the
// default branch (tracker.write).
func (ps *PullRequests) Open(ctx context.Context, ticketKey string) (PRView, error) {
	s, id, err := ps.scope(ctx, ticketKey, ActTrackerWrite)
	if err != nil {
		return PRView{}, err
	}
	return ps.ensure(ctx, s, actorIn(ctx, id))
}

// EnsureForTicket opens or refreshes a ticket's pull request for Core's
// orchestrator (no caller, no authorization).
func (ps *PullRequests) EnsureForTicket(ctx context.Context, it tracker.Item) (PRView, error) {
	s, err := ps.load(ctx, it)
	if err != nil {
		return PRView{}, err
	}
	cur, err := ps.Store.PullRequest(ctx, it.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		return ps.ensure(ctx, s, event.System)
	case err != nil:
		return PRView{}, err
	}
	pr, err := s.forge.Get(ctx, s.repo, cur.PullRequest)
	if err != nil {
		return PRView{}, ps.forgeErr(err)
	}
	if pr.State == cur.State && pr.Checks == cur.Checks && pr.Review == cur.Review && pr.HeadSHA == cur.HeadSHA {
		return PRView{TicketPR: cur, TicketKey: it.Key}, nil
	}
	return ps.save(ctx, s, pr, event.System)
}

// MergeForTicket squash-merges a ticket's open pull request for Core's
// orchestrator, when the ticket's policy allows it.
func (ps *PullRequests) MergeForTicket(ctx context.Context, it tracker.Item) (PRView, error) {
	s, err := ps.load(ctx, it)
	if err != nil {
		return PRView{}, err
	}
	cur, err := ps.Store.PullRequest(ctx, it.ID)
	if err != nil {
		return PRView{}, err
	}
	if err := s.forge.Merge(ctx, s.repo, cur.PullRequest, fmt.Sprintf("%s %s (#%d)", it.Key, it.Title, cur.Number)); err != nil {
		return PRView{}, ps.forgeErr(err)
	}
	pr, err := s.forge.Get(ctx, s.repo, cur.PullRequest)
	if err != nil {
		return PRView{}, ps.forgeErr(err)
	}
	return ps.save(ctx, s, pr, event.System)
}

func (ps *PullRequests) ensure(ctx context.Context, s prScope, actor event.Actor) (PRView, error) {
	branch, err := execution.BranchName(s.settings.BranchTemplate, s.ticket.Key, string(s.ticket.Type), s.ticket.Title)
	if err != nil {
		return PRView{}, invalid(err)
	}
	base := s.settings.DefaultBranch
	if base == "" {
		base = "main"
	}
	body := fmt.Sprintf("Ballet ticket **%s**: %s\n\n%s", s.ticket.Key, s.ticket.Title, s.ticket.Description)
	pr, err := s.forge.Ensure(ctx, s.repo, branch, base, s.ticket.Key+" "+s.ticket.Title, body)
	if err != nil {
		return PRView{}, ps.forgeErr(err)
	}
	return ps.save(ctx, s, pr, actor)
}

// Refresh reads the pull request's current state from the forge
// (tracker.read).
func (ps *PullRequests) Refresh(ctx context.Context, ticketKey string) (PRView, error) {
	s, id, err := ps.scope(ctx, ticketKey, ActTrackerRead)
	if err != nil {
		return PRView{}, err
	}
	cur, err := ps.Store.PullRequest(ctx, s.ticket.ID)
	if err != nil {
		return PRView{}, err
	}
	pr, err := s.forge.Get(ctx, s.repo, cur.PullRequest)
	if err != nil {
		return PRView{}, ps.forgeErr(err)
	}
	return ps.save(ctx, s, pr, actorIn(ctx, id))
}

// Merge squash-merges the ticket's pull request. Only humans merge here;
// automatic merging by policy comes with the orchestrator.
func (ps *PullRequests) Merge(ctx context.Context, ticketKey string) (PRView, error) {
	s, id, err := ps.scope(ctx, ticketKey, ActTrackerWrite)
	if err != nil {
		return PRView{}, err
	}
	if _, planner := PlannerSessionOf(ctx); planner || id.Kind != auth.KindHuman {
		return PRView{}, fmt.Errorf("%w: only a human can merge here", ErrForbidden)
	}
	cur, err := ps.Store.PullRequest(ctx, s.ticket.ID)
	if err != nil {
		return PRView{}, err
	}
	if cur.State != forge.StateOpen {
		return PRView{}, fmt.Errorf("%w: the pull request is %s", ErrConflict, cur.State)
	}
	if err := s.forge.Merge(ctx, s.repo, cur.PullRequest, fmt.Sprintf("%s %s (#%d)", s.ticket.Key, s.ticket.Title, cur.Number)); err != nil {
		return PRView{}, ps.forgeErr(err)
	}
	pr, err := s.forge.Get(ctx, s.repo, cur.PullRequest)
	if err != nil {
		return PRView{}, ps.forgeErr(err)
	}
	return ps.save(ctx, s, pr, actorIn(ctx, id))
}

func (ps *PullRequests) save(ctx context.Context, s prScope, pr forge.PullRequest, actor event.Actor) (PRView, error) {
	t := TicketPR{PullRequest: pr, TicketID: s.ticket.ID, ProjectID: s.project.ID, Forge: s.forge.Name()}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = ps.Now()
	}
	e := event.Event{Organization: s.organization.ID, Project: s.project.ID, EntityType: "item", EntityID: s.ticket.ID,
		Type: "item.pr_updated", Actor: actor, OccurredAt: ps.Now(),
		Payload: mustJSON(map[string]any{"number": pr.Number, "state": pr.State, "checks": pr.Checks, "review": pr.Review})}
	if err := ps.Store.SavePullRequest(ctx, t, &e); err != nil {
		return PRView{}, err
	}
	return PRView{TicketPR: t, TicketKey: s.ticket.Key}, nil
}

func (ps *PullRequests) forgeErr(err error) error {
	if errors.Is(err, forge.ErrNoBranch) {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	if errors.Is(err, forge.ErrUnsupported) {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return fmt.Errorf("%w: forge: %v", ErrUnavailable, err)
}

// Poll refreshes open pull requests every interval until ctx ends,
// recording an event when one changed.
func (ps *PullRequests) Poll(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		open, err := ps.Store.OpenPullRequests(ctx)
		if err != nil {
			ps.logger().ErrorContext(ctx, "list open pull requests failed", "error", err)
			continue
		}
		for _, cur := range open {
			it, err := ps.Items.ItemByID(ctx, cur.TicketID)
			if err != nil {
				continue
			}
			s, err := ps.load(ctx, it)
			if err != nil {
				continue
			}
			pr, err := s.forge.Get(ctx, s.repo, cur.PullRequest)
			if err != nil {
				ps.logger().WarnContext(ctx, "refresh pull request failed", "ticket", it.Key, "error", err)
				continue
			}
			if pr.State == cur.State && pr.Checks == cur.Checks && pr.Review == cur.Review && pr.HeadSHA == cur.HeadSHA {
				continue
			}
			if _, err := ps.save(ctx, s, pr, event.System); err != nil {
				ps.logger().ErrorContext(ctx, "save pull request failed", "ticket", it.Key, "error", err)
			}
		}
	}
}

// GitToken resolves a project's git token for Core's own forge calls.
func GitToken(cr *Credentials) func(ctx context.Context, organizationKey, projectKey string) (string, error) {
	return func(ctx context.Context, organizationKey, projectKey string) (string, error) {
		c, err := cr.Resolve(ctx, organizationKey, projectKey, credential.ProviderGit)
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return c.APIKey, err
	}
}

func (ps *PullRequests) logger() *slog.Logger {
	if ps.Logger != nil {
		return ps.Logger
	}
	return slog.Default()
}
