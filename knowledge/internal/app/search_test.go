package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/domain"
	"github.com/denyszorinets/ballet/knowledge/internal/store"
)

func as(t *testing.T, organization string, caps ...string) context.Context {
	return runtoken.ContextWithClaims(t.Context(), runtoken.Claims{
		Kind: runtoken.KindService, Subject: "service:core", Organization: organization, ActingFor: "bob", Capabilities: caps,
	})
}

type failingEmbedder struct{}

func (failingEmbedder) Model() string { return "broken" }
func (failingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("provider down")
}

func setup(t *testing.T) (*app.Service, *app.Indexer, *store.Store) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "k.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	emb := app.LocalEmbedders{Embedder: embed.Hash{}}
	ix := &app.Indexer{Store: st, Embedders: emb, Model: embed.HashModel}
	svc := &app.Service{Store: st, Searcher: st, Embedders: emb, Indexer: ix, Now: time.Now,
		NewID: func() string { return uuid.Must(uuid.NewV7()).String() }}
	return svc, ix, st
}

func create(t *testing.T, svc *app.Service, organization string, in app.CreateInput) domain.Entry {
	t.Helper()
	e, err := svc.Create(as(t, organization, runtoken.CapKnowledgeWrite), organization, in)
	require.NoError(t, err)
	return e
}

func search(t *testing.T, svc *app.Service, organization, text string, opts ...func(*app.SearchQuery)) []string {
	t.Helper()
	q := app.SearchQuery{Text: text}
	for _, o := range opts {
		o(&q)
	}
	hits, err := svc.Search(as(t, organization, runtoken.CapKnowledgeRead), organization, q)
	require.NoError(t, err)
	var titles []string
	for _, h := range hits {
		titles = append(titles, h.Entry.Title)
	}
	return titles
}

func TestIndexer_EmbedsNewChangedAndRemodelledEntries(t *testing.T) {
	svc, ix, st := setup(t)
	e := create(t, svc, "acme", app.CreateInput{Kind: domain.KindNote, Title: "Invoices", Body: "CSV export"})
	create(t, svc, "globex", app.CreateInput{Kind: domain.KindNote, Title: "Other", Body: "x"})

	n, err := ix.IndexOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	n, err = ix.IndexOnce(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n, "nothing stale")

	body := "CSV and PDF export"
	_, err = svc.Update(as(t, "acme", runtoken.CapKnowledgeWrite), "acme", e.ID, app.UpdateInput{Version: 1, Body: &body})
	require.NoError(t, err)
	n, _ = ix.IndexOnce(t.Context())
	assert.Equal(t, 1, n, "changed content is re-embedded")

	other := &app.Indexer{Store: st, Embedders: app.LocalEmbedders{Embedder: embed.Hash{}}, Model: "hash-v2"}
	n, _ = other.IndexOnce(t.Context())
	assert.Equal(t, 2, n, "a new model re-embeds everything")
}

func TestSearch_HybridWithinOneOrganization(t *testing.T) {
	svc, ix, _ := setup(t)
	create(t, svc, "acme", app.CreateInput{Kind: domain.KindDecision, Title: "Invoice export format", Body: "Invoices are exported as CSV files.", Projects: []string{"WEB"}})
	create(t, svc, "acme", app.CreateInput{Kind: domain.KindNote, Title: "Login", Body: "OIDC with Keycloak.", Projects: []string{"APP"}})
	create(t, svc, "acme", app.CreateInput{Kind: domain.KindDebt, Title: "Slow CSV generation", Body: "Export is slow for large invoices."})
	create(t, svc, "globex", app.CreateInput{Kind: domain.KindDecision, Title: "Invoice export at Globex", Body: "Invoices exported as CSV."})
	_, err := ix.IndexOnce(t.Context())
	require.NoError(t, err)

	got := search(t, svc, "acme", "invoice CSV export")
	require.NotEmpty(t, got)
	assert.Equal(t, "Invoice export format", got[0])
	assert.Contains(t, got, "Slow CSV generation")
	assert.NotContains(t, got, "Invoice export at Globex", "never another organization's entries")
	assert.NotContains(t, got, "Login")

	assert.Equal(t, []string{"Slow CSV generation"},
		search(t, svc, "acme", "invoice CSV export", func(q *app.SearchQuery) { q.Kind = domain.KindDebt }))
	assert.Equal(t, []string{"Invoice export format"},
		search(t, svc, "acme", "invoice CSV export", func(q *app.SearchQuery) { q.Project = "WEB" }))
	assert.Empty(t, search(t, svc, "acme", `"AND OR ( NEAR * -`), "user input cannot inject FTS syntax")
	assert.Empty(t, search(t, svc, "acme", "  "))
}

func TestSearch_FallsBackToFullTextWhenEmbeddingFails(t *testing.T) {
	svc, ix, _ := setup(t)
	create(t, svc, "acme", app.CreateInput{Kind: domain.KindNote, Title: "Invoices", Body: "CSV export"})
	_, _ = ix.IndexOnce(t.Context())
	svc.Embedders = app.LocalEmbedders{Embedder: failingEmbedder{}}

	assert.Equal(t, []string{"Invoices"}, search(t, svc, "acme", "csv"))
}

func TestSearch_RequiresReadCapabilityAndMatchingOrganization(t *testing.T) {
	svc, _, _ := setup(t)

	_, err := svc.Search(as(t, "globex", runtoken.CapKnowledgeRead), "acme", app.SearchQuery{Text: "x"})
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = svc.Search(as(t, "acme", runtoken.CapKnowledgeWrite), "acme", app.SearchQuery{Text: "x"})
	assert.ErrorIs(t, err, app.ErrForbidden)
}
