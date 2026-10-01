package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/kit/sqlstore"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/domain"
)

// SaveEmbedding stores (or replaces) the vector of an entry.
func (s *Store) SaveEmbedding(ctx context.Context, e domain.Entry, model, contentHash string, vector []float32) error {
	return s.db.Batch(ctx, sqlstore.Exec(`INSERT INTO entry_embeddings (entry_id, customer, model, content_hash, vector, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (entry_id) DO UPDATE SET model = excluded.model, content_hash = excluded.content_hash,
			vector = excluded.vector, updated_at = excluded.updated_at`,
		e.ID, e.Customer, model, contentHash, sqlstore.EncodeFloat32(vector), ts(time.Now())))
}

// StaleEmbeddings returns up to limit entries whose embedding is missing,
// of another model, or of older content (contentHash computes the hash).
func (s *Store) StaleEmbeddings(ctx context.Context, model string, limit int, contentHash func(domain.Entry) string) ([]domain.Entry, error) {
	rows, err := s.db.Query(ctx, `SELECT e.id, e.customer, e.kind, e.title, e.body, e.projects, e.items, e.version,
			e.created_by, e.updated_by, e.created_at, e.updated_at, COALESCE(x.model, ''), COALESCE(x.content_hash, '')
		FROM entries e LEFT JOIN entry_embeddings x ON x.entry_id = e.id`)
	if err != nil {
		return nil, fmt.Errorf("stale embeddings: %w", err)
	}
	defer rows.Close()
	var out []domain.Entry
	for rows.Next() && len(out) < limit {
		var xModel, xHash string
		e, err := scanWith(rows, &xModel, &xHash)
		if err != nil {
			return nil, err
		}
		if xModel != model || xHash != contentHash(e) {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

// Search runs the full-text and vector halves of a hybrid query for one
// customer and fuses them with reciprocal rank fusion (k = 60).
func (s *Store) Search(ctx context.Context, customer string, q app.SearchQuery) ([]app.SearchHit, error) {
	terms := embed.Words(q.Text)
	if len(terms) == 0 {
		return nil, nil
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + t + `"` // quoted terms: no FTS syntax from user input
	}
	where, args := []string{"e.customer = ?"}, []any{}
	if q.Kind != "" {
		where, args = append(where, "e.kind = ?"), append(args, string(q.Kind))
	}
	if q.Project != "" {
		where, args = append(where, "EXISTS (SELECT 1 FROM json_each(e.projects) WHERE value = ?)"), append(args, q.Project)
	}
	filter := strings.Join(where, " AND ")
	const candidates = 50

	sqlText := `
WITH fts AS (
  SELECT e.id AS id, row_number() OVER (ORDER BY bm25(entries_fts)) AS r
  FROM entries_fts JOIN entries e ON e.rowid = entries_fts.rowid
  WHERE entries_fts MATCH ? AND ` + filter + `
  ORDER BY bm25(entries_fts) LIMIT ` + fmt.Sprint(candidates) + `
)`
	fullArgs := append([]any{strings.Join(quoted, " OR "), customer}, args...)
	fused := `SELECT id, SUM(1.0 / (60 + r)) AS score FROM (SELECT * FROM fts) GROUP BY id`
	if q.Vector != nil {
		sqlText += `, vec AS (
  SELECT e.id AS id, row_number() OVER (ORDER BY vec_distance_cosine(x.vector, ?)) AS r
  FROM entry_embeddings x JOIN entries e ON e.id = x.entry_id
  WHERE x.model = ? AND vec_distance_cosine(x.vector, ?) <= ? AND ` + filter + `
  ORDER BY r LIMIT ` + fmt.Sprint(candidates) + `
)`
		vec := sqlstore.EncodeFloat32(q.Vector)
		fullArgs = append(fullArgs, vec, q.Model, vec, q.MaxDistance, customer)
		fullArgs = append(fullArgs, args...)
		fused = `SELECT id, SUM(1.0 / (60 + r)) AS score FROM (SELECT * FROM fts UNION ALL SELECT * FROM vec) GROUP BY id`
	}
	limit := q.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	sqlText += `
SELECT ` + prefixed("e.") + `, f.score FROM (` + fused + `) f JOIN entries e ON e.id = f.id
WHERE e.customer = ? ORDER BY f.score DESC, e.updated_at DESC LIMIT ?`
	fullArgs = append(fullArgs, customer, limit)

	rows, err := s.db.Query(ctx, sqlText, fullArgs...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var out []app.SearchHit
	for rows.Next() {
		var score float64
		e, err := scanWith(rows, &score)
		if err != nil {
			return nil, err
		}
		out = append(out, app.SearchHit{Entry: e, Score: score})
	}
	return out, rows.Err()
}

func prefixed(p string) string {
	cs := strings.Split(cols, ",")
	for i, c := range cs {
		cs[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(cs, ", ")
}

// scanWith scans an entry followed by extra columns.
func scanWith(r scanner, extra ...any) (domain.Entry, error) {
	var e domain.Entry
	var kind, projects, items, created, updated string
	dest := append([]any{&e.ID, &e.Customer, &kind, &e.Title, &e.Body, &projects, &items, &e.Version,
		&e.CreatedBy, &e.UpdatedBy, &created, &updated}, extra...)
	if err := r.Scan(dest...); err != nil {
		return domain.Entry{}, err
	}
	return fill(e, kind, projects, items, created, updated), nil
}
