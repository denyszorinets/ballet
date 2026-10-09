package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// UpsertSearchDoc stores a search document.
func (s *Store) UpsertSearchDoc(ctx context.Context, d app.SearchDoc) error {
	return s.db.Batch(ctx, sqlstore.Exec(`INSERT INTO search_docs
			(id, kind, entity_id, ref, organization_key, project_key, scope, title, body, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET ref = excluded.ref, organization_key = excluded.organization_key,
			project_key = excluded.project_key, scope = excluded.scope, title = excluded.title,
			body = excluded.body, updated_at = excluded.updated_at`,
		d.ID, d.Kind, d.EntityID, d.Ref, d.Organization, d.Project, d.Scope, d.Title, d.Body, formatTime(d.UpdatedAt)))
}

// SearchCursor returns the last event seq the search indexer processed.
func (s *Store) SearchCursor(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRow(ctx, `SELECT value FROM search_state WHERE name = 'event_cursor'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// SetSearchCursor stores the indexer position.
func (s *Store) SetSearchCursor(ctx context.Context, seq int64) error {
	return s.db.Batch(ctx, sqlstore.Exec(`INSERT INTO search_state (name, value) VALUES ('event_cursor', ?)
		ON CONFLICT (name) DO UPDATE SET value = excluded.value`, seq))
}

// StaleSearchDocs returns up to limit documents whose embedding is
// missing, of another model or of older content.
func (s *Store) StaleSearchDocs(ctx context.Context, model string, limit int, hash func(app.SearchDoc) string) ([]app.SearchDoc, error) {
	rows, err := s.db.Query(ctx, `SELECT d.id, d.kind, d.entity_id, d.ref, d.organization_key, d.project_key, d.scope,
			d.title, d.body, COALESCE(e.model, ''), COALESCE(e.content_hash, '')
		FROM search_docs d LEFT JOIN search_embeddings e ON e.doc_id = d.id`)
	if err != nil {
		return nil, fmt.Errorf("stale search docs: %w", err)
	}
	defer rows.Close()
	var out []app.SearchDoc
	for rows.Next() && len(out) < limit {
		var d app.SearchDoc
		var m, h string
		if err := rows.Scan(&d.ID, &d.Kind, &d.EntityID, &d.Ref, &d.Organization, &d.Project, &d.Scope, &d.Title, &d.Body, &m, &h); err != nil {
			return nil, err
		}
		if m != model || h != hash(d) {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// SaveSearchEmbedding stores a document's vector.
func (s *Store) SaveSearchEmbedding(ctx context.Context, docID, model, hash string, v []float32) error {
	return s.db.Batch(ctx, sqlstore.Exec(`INSERT INTO search_embeddings (doc_id, model, content_hash, vector)
		VALUES (?, ?, ?, ?) ON CONFLICT (doc_id) DO UPDATE SET model = excluded.model,
		content_hash = excluded.content_hash, vector = excluded.vector`, docID, model, hash, sqlstore.EncodeFloat32(v)))
}

// SearchDocs runs a hybrid query (full-text + optional vector), fused by
// reciprocal rank, returning up to limit candidates. Authorization is the
// caller's job.
func (s *Store) SearchDocs(ctx context.Context, q app.DocQuery) ([]app.DocHit, error) {
	terms := embed.Words(q.Text)
	if len(terms) == 0 {
		return nil, nil
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + t + `"`
	}
	where, args := []string{"1 = 1"}, []any{}
	if q.Kind != "" {
		where, args = append(where, "d.kind = ?"), append(args, q.Kind)
	}
	if q.Project != "" {
		// The project's own documents plus skills of its chain.
		where = append(where, "(d.project_key = ? OR (d.kind = 'skill' AND (d.scope = 'platform' OR d.scope = ?)))")
		args = append(args, q.Project, "organization:"+q.Organization)
	}
	filter := strings.Join(where, " AND ")
	const candidates = 100
	sqlText := `WITH fts AS (
  SELECT d.id AS id, row_number() OVER (ORDER BY bm25(search_fts)) AS r
  FROM search_fts JOIN search_docs d ON d.rowid = search_fts.rowid
  WHERE search_fts MATCH ? AND ` + filter + ` ORDER BY bm25(search_fts) LIMIT ` + fmt.Sprint(candidates) + `)`
	all := append([]any{strings.Join(quoted, " OR ")}, args...)
	fused := `SELECT id, SUM(1.0 / (60 + r)) AS score FROM (SELECT * FROM fts) GROUP BY id`
	if q.Vector != nil {
		vec := sqlstore.EncodeFloat32(q.Vector)
		sqlText += `, vec AS (
  SELECT d.id AS id, row_number() OVER (ORDER BY vec_distance_cosine(e.vector, ?)) AS r
  FROM search_embeddings e JOIN search_docs d ON d.id = e.doc_id
  WHERE e.model = ? AND vec_distance_cosine(e.vector, ?) <= ? AND ` + filter + `
  ORDER BY r LIMIT ` + fmt.Sprint(candidates) + `)`
		all = append(append(all, vec, q.Model, vec, q.MaxDistance), args...)
		fused = `SELECT id, SUM(1.0 / (60 + r)) AS score FROM (SELECT * FROM fts UNION ALL SELECT * FROM vec) GROUP BY id`
	}
	sqlText += `
SELECT d.id, d.kind, d.entity_id, d.ref, d.organization_key, d.project_key, d.scope, d.title, d.body, f.score
FROM (` + fused + `) f JOIN search_docs d ON d.id = f.id ORDER BY f.score DESC LIMIT ` + fmt.Sprint(candidates)
	rows, err := s.db.Query(ctx, sqlText, all...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var out []app.DocHit
	for rows.Next() {
		var h app.DocHit
		if err := rows.Scan(&h.ID, &h.Kind, &h.EntityID, &h.Ref, &h.Organization, &h.Project, &h.Scope, &h.Title, &h.Body, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
