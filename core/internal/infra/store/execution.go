package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// ExecutionSettings returns a project's execution settings (ErrNotFound
// when never set).
func (s *Store) ExecutionSettings(ctx context.Context, projectID string) (execution.Settings, error) {
	var x execution.Settings
	var setup, env, updated string
	err := s.db.QueryRow(ctx, `SELECT project_id, repo_url, default_branch, image, setup, env, branch_template,
			git_name, git_email, updated_at, version, forge, forge_api_url, link_template FROM project_execution
			WHERE project_id = ?`, projectID).
		Scan(&x.ProjectID, &x.RepoURL, &x.DefaultBranch, &x.Image, &setup, &env, &x.BranchTemplate, &x.GitName,
			&x.GitEmail, &updated, &x.Version, &x.Forge, &x.ForgeAPIURL, &x.LinkTemplate)
	if err != nil {
		return execution.Settings{}, mapReadErr("execution settings", err)
	}
	if err := json.Unmarshal([]byte(setup), &x.Setup); err != nil {
		return execution.Settings{}, err
	}
	if err := json.Unmarshal([]byte(env), &x.Env); err != nil {
		return execution.Settings{}, err
	}
	x.UpdatedAt, err = parseTime(updated)
	return x, err
}

// SetExecutionSettings stores x if the stored settings are at
// expectedVersion (0: none yet), and records e.
func (s *Store) SetExecutionSettings(ctx context.Context, x execution.Settings, expectedVersion int64, e event.Event) error {
	setup, err := json.Marshal(nonNil(x.Setup))
	if err != nil {
		return fmt.Errorf("set execution settings: %w", err)
	}
	if x.Env == nil {
		x.Env = map[string]string{}
	}
	env, err := json.Marshal(x.Env)
	if err != nil {
		return fmt.Errorf("set execution settings: %w", err)
	}
	args := []any{x.RepoURL, x.DefaultBranch, x.Image, string(setup), string(env), x.BranchTemplate, x.GitName, x.GitEmail,
		formatTime(x.UpdatedAt), x.Version, x.Forge, x.ForgeAPIURL, x.LinkTemplate}
	var stmt sqlstore.Stmt
	if expectedVersion == 0 {
		stmt = sqlstore.Exec(`INSERT INTO project_execution (project_id, repo_url, default_branch, image, setup, env,
			branch_template, git_name, git_email, updated_at, version, forge, forge_api_url, link_template)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			append([]any{x.ProjectID}, args...)...)
	} else {
		stmt = sqlstore.ExecOne(`UPDATE project_execution SET repo_url = ?, default_branch = ?, image = ?, setup = ?,
			env = ?, branch_template = ?, git_name = ?, git_email = ?, updated_at = ?, version = ?, forge = ?,
			forge_api_url = ?, link_template = ? WHERE project_id = ? AND version = ?`, append(args, x.ProjectID, expectedVersion)...)
	}
	return mapWriteErr("set execution settings", s.db.Batch(ctx, stmt, s.AppendEvent(e)))
}
