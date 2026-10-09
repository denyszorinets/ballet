package store

import (
	"context"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/app"
)

// DeliveryGauges counts active flows, open questions and active runs by
// organization and project key.
func (s *Store) DeliveryGauges(ctx context.Context) (app.DeliveryGauges, error) {
	var g app.DeliveryGauges
	var err error
	if g.Flows, err = s.gaugeRows(ctx, `SELECT c.key, p.key, f.status, f.waiting, count(*) FROM flows f
		JOIN projects p ON p.id = f.project_id JOIN organizations c ON c.id = p.organization_id
		WHERE f.status IN ('running', 'waiting') GROUP BY 1, 2, 3, 4`); err != nil {
		return g, err
	}
	if g.Questions, err = s.gaugeRows(ctx, `SELECT c.key, p.key, q.route, '', count(*) FROM questions q
		JOIN projects p ON p.id = q.project_id JOIN organizations c ON c.id = p.organization_id
		WHERE q.status = 'open' GROUP BY 1, 2, 3`); err != nil {
		return g, err
	}
	g.Runs, err = s.gaugeRows(ctx, `SELECT c.key, p.key, r.status, '', count(*) FROM runs r
		JOIN projects p ON p.id = r.project_id JOIN organizations c ON c.id = p.organization_id
		WHERE r.status IN ('queued', 'starting', 'running') GROUP BY 1, 2, 3`)
	return g, err
}

func (s *Store) gaugeRows(ctx context.Context, query string) ([]app.GaugeRow, error) {
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("delivery gauges: %w", err)
	}
	defer rows.Close()
	var out []app.GaugeRow
	for rows.Next() {
		var r app.GaugeRow
		if err := rows.Scan(&r.Organization, &r.Project, &r.State, &r.Detail, &r.Count); err != nil {
			return nil, fmt.Errorf("delivery gauges: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
