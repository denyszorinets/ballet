package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/skill"
	"github.com/denyszorinets/ballet/kit/embed"
)

// SearchDoc is a searchable view of a Core entity.
type SearchDoc struct {
	ID        string // "<kind>:<entity id>"
	Kind      string // "item" or "skill"
	EntityID  string
	Ref       string // item key or skill name
	Customer  string // customer key; "" for platform skills
	Project   string // project key; "" when not project-specific
	Scope     string // skills: their scope
	Title     string
	Body      string
	UpdatedAt time.Time
}

// DocQuery is a hybrid query over search documents.
type DocQuery struct {
	Text, Kind        string
	Project, Customer string // restrict to a project (and its skill chain)
	Vector            []float32
	Model             string
	MaxDistance       float64
}

// DocHit is a candidate with its fused score.
type DocHit struct {
	SearchDoc
	Score float64
}

// SearchStore persists the search index.
type SearchStore interface {
	UpsertSearchDoc(ctx context.Context, d SearchDoc) error
	SearchCursor(ctx context.Context) (int64, error)
	SetSearchCursor(ctx context.Context, seq int64) error
	StaleSearchDocs(ctx context.Context, model string, limit int, hash func(SearchDoc) string) ([]SearchDoc, error)
	SaveSearchEmbedding(ctx context.Context, docID, model, hash string, v []float32) error
	SearchDocs(ctx context.Context, q DocQuery) ([]DocHit, error)
}

func docHash(d SearchDoc) string {
	sum := sha256.Sum256([]byte(d.Title + "\n" + d.Body))
	return hex.EncodeToString(sum[:8])
}

// SearchIndexer keeps search documents and embeddings current by tailing
// the event log from a persisted cursor.
type SearchIndexer struct {
	Store    SearchStore
	Log      EventLog
	Items    ItemStore
	Skills   SkillStore
	Tenancy  TenancyStore
	Embedder embed.Embedder
	Logger   *slog.Logger
	Interval time.Duration // default 2s
}

// Run indexes until ctx is cancelled.
func (ix *SearchIndexer) Run(ctx context.Context) {
	interval := ix.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		if err := ix.IndexOnce(ctx); err != nil && ix.Logger != nil {
			ix.Logger.WarnContext(ctx, "search indexing failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// IndexOnce processes new events, then embeds stale documents.
func (ix *SearchIndexer) IndexOnce(ctx context.Context) error {
	for {
		cursor, err := ix.Store.SearchCursor(ctx)
		if err != nil {
			return err
		}
		events, err := ix.Log.QueryEvents(ctx, EventQuery{AfterSeq: cursor, Limit: 500})
		if err != nil || len(events) == 0 {
			if err != nil {
				return err
			}
			break
		}
		for _, e := range events {
			if err := ix.indexEntity(ctx, e.EntityType, e.EntityID); err != nil {
				return err
			}
		}
		if err := ix.Store.SetSearchCursor(ctx, events[len(events)-1].Seq); err != nil {
			return err
		}
	}
	for {
		stale, err := ix.Store.StaleSearchDocs(ctx, ix.Embedder.Model(), 100, docHash)
		if err != nil || len(stale) == 0 {
			return err
		}
		texts := make([]string, len(stale))
		for i, d := range stale {
			texts[i] = d.Title + "\n" + d.Body
		}
		vs, err := ix.Embedder.Embed(ctx, texts)
		if err != nil {
			return err
		}
		for i, d := range stale {
			if err := ix.Store.SaveSearchEmbedding(ctx, d.ID, ix.Embedder.Model(), docHash(d), vs[i]); err != nil {
				return err
			}
		}
	}
}

func (ix *SearchIndexer) indexEntity(ctx context.Context, entityType, id string) error {
	switch entityType {
	case "item":
		it, err := ix.Items.ItemByID(ctx, id)
		if err != nil {
			return nil // deleted or not yet visible: nothing to index
		}
		p, err := ix.Tenancy.ProjectByID(ctx, it.ProjectID)
		if err != nil {
			return nil
		}
		c, err := ix.Tenancy.CustomerByID(ctx, p.CustomerID)
		if err != nil {
			return nil
		}
		body := it.Description
		if len(it.AcceptanceCriteria) > 0 {
			body += "\n" + strings.Join(it.AcceptanceCriteria, "\n")
		}
		return ix.Store.UpsertSearchDoc(ctx, SearchDoc{
			ID: "item:" + it.ID, Kind: "item", EntityID: it.ID, Ref: it.Key, Customer: c.Key, Project: p.Key,
			Title: it.Title, Body: body, UpdatedAt: it.UpdatedAt,
		})
	case "skill":
		s, err := ix.Skills.Skill(ctx, id)
		if err != nil {
			return nil
		}
		return ix.Store.UpsertSearchDoc(ctx, SearchDoc{
			ID: "skill:" + s.ID, Kind: "skill", EntityID: s.ID, Ref: s.Name, Customer: s.Scope.Customer,
			Project: s.Scope.Project, Scope: s.Scope.String(),
			Title: s.Name + ": " + s.Draft.Description, Body: s.Draft.Body, UpdatedAt: s.UpdatedAt,
		})
	}
	return nil
}

// Search answers search queries with authorization applied per result.
type Search struct {
	Store       SearchStore
	Tenancy     TenancyStore
	Authz       Authorizer
	Embedder    embed.Embedder // nil: full-text only
	MaxDistance float64
}

// SearchResult is an authorized hit.
type SearchResult = DocHit

// Query searches items and skills the caller may read. kind ("item" or
// "skill") and projectKey narrow the search; limit defaults to 20.
func (s *Search) Query(ctx context.Context, text, kind, projectKey string, limit int) ([]SearchResult, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	q := DocQuery{Text: text, Kind: kind, MaxDistance: s.MaxDistance}
	if q.MaxDistance <= 0 {
		q.MaxDistance = 0.85
	}
	if projectKey != "" {
		p, err := s.Tenancy.ProjectByKey(ctx, projectKey)
		if err != nil {
			return nil, err
		}
		c, err := s.Tenancy.CustomerByID(ctx, p.CustomerID)
		if err != nil {
			return nil, err
		}
		q.Project, q.Customer = p.Key, c.Key
	}
	if s.Embedder != nil {
		if vs, err := s.Embedder.Embed(ctx, []string{text}); err == nil {
			q.Vector, q.Model = vs[0], s.Embedder.Model()
		}
	}
	hits, err := s.Store.SearchDocs(ctx, q)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	allowed := map[string]bool{}
	out := []SearchResult{}
	for _, h := range hits {
		action, target := ActTrackerRead, Scope{Customer: h.Customer, Project: h.Project}
		if h.Kind == "skill" {
			sc, _ := skill.ParseScope(h.Scope)
			action, target = ActSkillRead, Scope{Customer: h.Customer, Project: sc.Project}
		}
		key := string(action) + "|" + target.Customer + "|" + target.Project
		ok, seen := allowed[key]
		if !seen {
			ok = s.Authz.Authorize(ctx, id, action, target) == nil
			allowed[key] = ok
		}
		if ok {
			out = append(out, h)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
