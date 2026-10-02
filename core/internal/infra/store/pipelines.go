package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// SavePipelineVersion stores version p.Version of a pipeline if the
// latest stored version is p.Version-1 (ErrConflict otherwise), and
// records e.
func (s *Store) SavePipelineVersion(ctx context.Context, p app.PipelineVersion, e event.Event) error {
	def, err := json.Marshal(p.Definition)
	if err != nil {
		return fmt.Errorf("save pipeline: %w", err)
	}
	return mapWriteErr("save pipeline", s.db.Batch(ctx,
		sqlstore.ExecOne(`INSERT INTO pipelines (project_id, name, version, definition, created_by, created_at)
			SELECT ?, ?, ?, ?, ?, ? WHERE (SELECT COALESCE(MAX(version), 0) FROM pipelines
				WHERE project_id = ? AND name = ?) = ?`,
			p.ProjectID, p.Name, p.Version, string(def), p.CreatedBy, formatTime(p.CreatedAt),
			p.ProjectID, p.Name, p.Version-1),
		s.AppendEvent(e),
	))
}

const pipelineCols = `project_id, name, version, definition, created_by, created_at`

// PipelineVersion returns a version of a pipeline; version 0: the latest.
func (s *Store) PipelineVersion(ctx context.Context, projectID, name string, version int64) (app.PipelineVersion, error) {
	q := `SELECT ` + pipelineCols + ` FROM pipelines WHERE project_id = ? AND name = ? AND version = ?`
	args := []any{projectID, name, version}
	if version == 0 {
		q = `SELECT ` + pipelineCols + ` FROM pipelines WHERE project_id = ? AND name = ? ORDER BY version DESC LIMIT 1`
		args = args[:2]
	}
	p, err := scanPipeline(s.db.QueryRow(ctx, q, args...))
	return p, mapReadErr("pipeline "+name, err)
}

// PipelineVersions returns all versions of a pipeline, newest first.
func (s *Store) PipelineVersions(ctx context.Context, projectID, name string) ([]app.PipelineVersion, error) {
	return s.queryPipelines(ctx, `SELECT `+pipelineCols+` FROM pipelines WHERE project_id = ? AND name = ?
		ORDER BY version DESC`, projectID, name)
}

// LatestPipelines returns the latest version of each of a project's
// pipelines, by name.
func (s *Store) LatestPipelines(ctx context.Context, projectID string) ([]app.PipelineVersion, error) {
	return s.queryPipelines(ctx, `SELECT `+pipelineCols+` FROM pipelines p WHERE project_id = ?
		AND version = (SELECT MAX(version) FROM pipelines q WHERE q.project_id = p.project_id AND q.name = p.name)
		ORDER BY name`, projectID)
}

func (s *Store) queryPipelines(ctx context.Context, q string, args ...any) ([]app.PipelineVersion, error) {
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("pipelines: %w", err)
	}
	defer rows.Close()
	var out []app.PipelineVersion
	for rows.Next() {
		p, err := scanPipeline(rows)
		if err != nil {
			return nil, fmt.Errorf("pipelines: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPipeline(r scanner) (app.PipelineVersion, error) {
	var p app.PipelineVersion
	var def, created string
	if err := r.Scan(&p.ProjectID, &p.Name, &p.Version, &def, &p.CreatedBy, &created); err != nil {
		return app.PipelineVersion{}, err
	}
	if err := json.Unmarshal([]byte(def), &p.Definition); err != nil {
		return app.PipelineVersion{}, err
	}
	var err error
	p.CreatedAt, err = parseTime(created)
	return p, err
}
