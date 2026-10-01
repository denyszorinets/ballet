package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/skill"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

func filesJSON(f map[string]string) string {
	if f == nil {
		f = map[string]string{}
	}
	b, _ := json.Marshal(f)
	return string(b)
}

// CreateSkill inserts a skill (draft only) and records e.
func (s *Store) CreateSkill(ctx context.Context, sk skill.Skill, customerID string, e event.Event) error {
	return mapWriteErr("create skill", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO skills (id, scope_kind, customer_key, project_key, customer_id, name, description, body,
				files, latest_version, created_at, updated_at, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sk.ID, string(sk.Scope.Kind), sk.Scope.Customer, sk.Scope.Project, customerID, sk.Name, sk.Draft.Description,
			sk.Draft.Body, filesJSON(sk.Draft.Files), sk.LatestVersion, formatTime(sk.CreatedAt), formatTime(sk.UpdatedAt), sk.Version),
		s.AppendEvent(e),
	))
}

// UpdateSkillDraft stores the draft if the skill is at expectedVersion.
func (s *Store) UpdateSkillDraft(ctx context.Context, sk skill.Skill, expectedVersion int64, e event.Event) error {
	return mapWriteErr("update skill", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE skills SET description = ?, body = ?, files = ?, updated_at = ?, version = ?
			WHERE id = ? AND version = ?`,
			sk.Draft.Description, sk.Draft.Body, filesJSON(sk.Draft.Files), formatTime(sk.UpdatedAt), sk.Version, sk.ID, expectedVersion),
		s.AppendEvent(e),
	))
}

// PublishSkill stores v as the next version if the skill is at
// expectedVersion.
func (s *Store) PublishSkill(ctx context.Context, sk skill.Skill, v skill.Version, expectedVersion int64, e event.Event) error {
	return mapWriteErr("publish skill", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE skills SET latest_version = ?, updated_at = ?, version = ? WHERE id = ? AND version = ?`,
			v.Number, formatTime(sk.UpdatedAt), sk.Version, sk.ID, expectedVersion),
		sqlstore.Exec(`INSERT INTO skill_versions (skill_id, number, description, body, files, published_by, published_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, sk.ID, v.Number, v.Content.Description, v.Content.Body, filesJSON(v.Content.Files),
			v.PublishedBy, formatTime(v.PublishedAt)),
		s.AppendEvent(e),
	))
}

const skillCols = `id, scope_kind, customer_key, project_key, name, description, body, files, latest_version,
	created_at, updated_at, version`

// Skill returns a skill by ID.
func (s *Store) Skill(ctx context.Context, id string) (skill.Skill, error) {
	sk, err := scanSkill(s.db.QueryRow(ctx, `SELECT `+skillCols+` FROM skills WHERE id = ?`, id))
	return sk, mapReadErr("skill "+id, err)
}

// ListSkills returns the skills of exactly one scope, by name.
func (s *Store) ListSkills(ctx context.Context, scope skill.Scope) ([]skill.Skill, error) {
	return s.querySkills(ctx, `WHERE scope_kind = ? AND customer_key = ? AND project_key = ? ORDER BY name`,
		string(scope.Kind), scope.Customer, scope.Project)
}

// SkillsInScopes returns the skills of several scopes (resolution).
func (s *Store) SkillsInScopes(ctx context.Context, scopes []skill.Scope) ([]skill.Skill, error) {
	var conds []string
	var args []any
	for _, sc := range scopes {
		conds = append(conds, "(scope_kind = ? AND customer_key = ? AND project_key = ?)")
		args = append(args, string(sc.Kind), sc.Customer, sc.Project)
	}
	if len(conds) == 0 {
		return nil, nil
	}
	return s.querySkills(ctx, `WHERE `+strings.Join(conds, " OR ")+` ORDER BY name`, args...)
}

func (s *Store) querySkills(ctx context.Context, where string, args ...any) ([]skill.Skill, error) {
	rows, err := s.db.Query(ctx, `SELECT `+skillCols+` FROM skills `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()
	var out []skill.Skill
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("list skills: %w", err)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// SkillVersions returns a skill's published versions, newest first.
func (s *Store) SkillVersions(ctx context.Context, skillID string) ([]skill.Version, error) {
	rows, err := s.db.Query(ctx, `SELECT number, description, body, files, published_by, published_at
		FROM skill_versions WHERE skill_id = ? ORDER BY number DESC`, skillID)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	defer rows.Close()
	var out []skill.Version
	for rows.Next() {
		v, err := scanSkillVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SkillVersion returns one published version.
func (s *Store) SkillVersion(ctx context.Context, skillID string, number int64) (skill.Version, error) {
	v, err := scanSkillVersion(s.db.QueryRow(ctx, `SELECT number, description, body, files, published_by, published_at
		FROM skill_versions WHERE skill_id = ? AND number = ?`, skillID, number))
	return v, mapReadErr(fmt.Sprintf("skill version %d", number), err)
}

func scanSkill(r scanner) (skill.Skill, error) {
	var sk skill.Skill
	var kind, files, created, updated string
	if err := r.Scan(&sk.ID, &kind, &sk.Scope.Customer, &sk.Scope.Project, &sk.Name, &sk.Draft.Description,
		&sk.Draft.Body, &files, &sk.LatestVersion, &created, &updated, &sk.Version); err != nil {
		return skill.Skill{}, err
	}
	sk.Scope.Kind = skill.ScopeKind(kind)
	if err := json.Unmarshal([]byte(files), &sk.Draft.Files); err != nil {
		return skill.Skill{}, err
	}
	var err error
	if sk.CreatedAt, err = parseTime(created); err != nil {
		return skill.Skill{}, err
	}
	sk.UpdatedAt, err = parseTime(updated)
	return sk, err
}

func scanSkillVersion(r scanner) (skill.Version, error) {
	var v skill.Version
	var files, published string
	if err := r.Scan(&v.Number, &v.Content.Description, &v.Content.Body, &files, &v.PublishedBy, &published); err != nil {
		return skill.Version{}, err
	}
	if err := json.Unmarshal([]byte(files), &v.Content.Files); err != nil {
		return skill.Version{}, err
	}
	var err error
	v.PublishedAt, err = parseTime(published)
	return v, err
}
