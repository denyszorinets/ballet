package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/knowledge/internal/domain"
)

// Embedders returns the embedder to use for an organization's content (its
// credentials and metering apply).
type Embedders interface {
	For(organization string) embed.Embedder
}

// LocalEmbedders uses the same embedder for every organization.
type LocalEmbedders struct{ Embedder embed.Embedder }

// For returns the shared embedder.
func (l LocalEmbedders) For(string) embed.Embedder { return l.Embedder }

// GatewayEmbedders embeds through the LLM gateway on behalf of each organization.
type GatewayEmbedders struct {
	URL   string
	Token func(context.Context) (string, error)
	Model string
}

// For returns a gateway client attributed to organization.
func (g GatewayEmbedders) For(organization string) embed.Embedder {
	return &embed.Gateway{URL: g.URL, Token: g.Token, ModelID: g.Model, Organization: organization}
}

// SearchStore runs hybrid queries.
type SearchStore interface {
	Search(ctx context.Context, organization string, q SearchQuery) ([]SearchHit, error)
}

// Search finds entries of organization's space matching text: full-text and,
// when an embedder is configured, semantic similarity, fused by rank.
func (s *Service) Search(ctx context.Context, organization string, q SearchQuery) ([]SearchHit, error) {
	if _, err := authorize(ctx, organization, runtoken.CapKnowledgeRead); err != nil {
		return nil, err
	}
	q.Vector, q.Model, q.MaxDistance = nil, "", s.MaxDistance
	if q.MaxDistance <= 0 {
		q.MaxDistance = DefaultMaxDistance
	}
	if s.Embedders != nil {
		emb := s.Embedders.For(organization)
		vs, err := emb.Embed(ctx, []string{q.Text})
		if err != nil {
			slog.WarnContext(ctx, "embedding the query failed; full-text search only", "error", err)
		} else {
			q.Vector, q.Model = vs[0], emb.Model()
		}
	}
	return s.Searcher.Search(ctx, organization, q)
}

// ContentHash identifies the embedded content of an entry.
func ContentHash(e domain.Entry) string {
	sum := sha256.Sum256([]byte(e.Title + "\n" + e.Body))
	return hex.EncodeToString(sum[:8])
}

// IndexStore stores embeddings.
type IndexStore interface {
	SaveEmbedding(ctx context.Context, e domain.Entry, model, contentHash string, vector []float32) error
	StaleEmbeddings(ctx context.Context, model string, limit int, contentHash func(domain.Entry) string) ([]domain.Entry, error)
}

// Indexer keeps entry embeddings current: it embeds new and changed
// entries asynchronously and re-embeds everything when the model changes.
type Indexer struct {
	Store     IndexStore
	Embedders Embedders
	Model     string // the model Embedders produce
	Logger    *slog.Logger
	// Idle is how often to re-check when nothing changed (default 30s).
	Idle time.Duration

	wake chan struct{}
}

// EntryChanged schedules indexing; it never blocks.
func (ix *Indexer) EntryChanged(domain.Entry) {
	ix.init()
	select {
	case ix.wake <- struct{}{}:
	default:
	}
}

func (ix *Indexer) init() {
	if ix.wake == nil {
		ix.wake = make(chan struct{}, 1)
	}
}

// Run indexes until ctx is cancelled.
func (ix *Indexer) Run(ctx context.Context) {
	ix.init()
	idle := ix.Idle
	if idle <= 0 {
		idle = 30 * time.Second
	}
	for {
		n, err := ix.IndexOnce(ctx)
		if err != nil && ix.Logger != nil {
			ix.Logger.WarnContext(ctx, "indexing failed", "error", err)
		}
		if n > 0 && err == nil {
			continue // more may be pending
		}
		select {
		case <-ctx.Done():
			return
		case <-ix.wake:
		case <-time.After(idle):
		}
	}
}

// IndexOnce embeds one batch of stale entries and returns how many.
func (ix *Indexer) IndexOnce(ctx context.Context) (int, error) {
	stale, err := ix.Store.StaleEmbeddings(ctx, ix.Model, 50, ContentHash)
	if err != nil || len(stale) == 0 {
		return 0, err
	}
	byOrganization := map[string][]domain.Entry{}
	for _, e := range stale {
		byOrganization[e.Organization] = append(byOrganization[e.Organization], e)
	}
	done := 0
	for organization, entries := range byOrganization {
		texts := make([]string, len(entries))
		for i, e := range entries {
			texts[i] = e.Title + "\n" + e.Body
		}
		vs, err := ix.Embedders.For(organization).Embed(ctx, texts)
		if err != nil {
			return done, err
		}
		for i, e := range entries {
			if err := ix.Store.SaveEmbedding(ctx, e, ix.Model, ContentHash(e), vs[i]); err != nil {
				return done, err
			}
			done++
		}
	}
	return done, nil
}
