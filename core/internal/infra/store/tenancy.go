package store

import (
	"context"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// CreateOrganization inserts c and records e.
func (s *Store) CreateOrganization(ctx context.Context, c tenancy.Organization, e event.Event) error {
	return mapWriteErr("create organization", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO organizations (id, key, name, feature_policy, created_at, updated_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, c.ID, c.Key, c.Name, policyOrDefault(c.FeaturePolicy),
			formatTime(c.CreatedAt), formatTime(c.UpdatedAt), c.Version),
		s.AppendEvent(e),
	))
}

// UpdateOrganization stores c if the stored version equals expectedVersion.
func (s *Store) UpdateOrganization(ctx context.Context, c tenancy.Organization, expectedVersion int64, e event.Event) error {
	return mapWriteErr("update organization", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE organizations SET name = ?, feature_policy = ?, updated_at = ?, version = ?
			WHERE id = ? AND version = ?`, c.Name, policyOrDefault(c.FeaturePolicy), formatTime(c.UpdatedAt), c.Version,
			c.ID, expectedVersion),
		s.AppendEvent(e),
	))
}

const organizationCols = `id, key, name, feature_policy, created_at, updated_at, version`

// OrganizationByKey returns the organization with key.
func (s *Store) OrganizationByKey(ctx context.Context, key string) (tenancy.Organization, error) {
	c, err := scanOrganization(s.db.QueryRow(ctx, `SELECT `+organizationCols+` FROM organizations WHERE key = ?`, key))
	return c, mapReadErr("organization "+key, err)
}

// OrganizationByID returns the organization with id.
func (s *Store) OrganizationByID(ctx context.Context, id string) (tenancy.Organization, error) {
	c, err := scanOrganization(s.db.QueryRow(ctx, `SELECT `+organizationCols+` FROM organizations WHERE id = ?`, id))
	return c, mapReadErr("organization "+id, err)
}

// ListOrganizations returns all organizations ordered by key.
func (s *Store) ListOrganizations(ctx context.Context) ([]tenancy.Organization, error) {
	rows, err := s.db.Query(ctx, `SELECT `+organizationCols+` FROM organizations ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()
	var out []tenancy.Organization
	for rows.Next() {
		c, err := scanOrganization(rows)
		if err != nil {
			return nil, fmt.Errorf("list organizations: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateProject inserts p and records e.
func (s *Store) CreateProject(ctx context.Context, p tenancy.Project, e event.Event) error {
	return mapWriteErr("create project", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO projects (id, organization_id, key, name, description, created_at, updated_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, p.ID, p.OrganizationID, p.Key, p.Name, p.Description,
			formatTime(p.CreatedAt), formatTime(p.UpdatedAt), p.Version),
		s.AppendEvent(e),
	))
}

// UpdateProject stores p if the stored version equals expectedVersion.
func (s *Store) UpdateProject(ctx context.Context, p tenancy.Project, expectedVersion int64, e event.Event) error {
	return mapWriteErr("update project", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE projects SET name = ?, description = ?, updated_at = ?, version = ?
			WHERE id = ? AND version = ?`, p.Name, p.Description, formatTime(p.UpdatedAt), p.Version, p.ID, expectedVersion),
		s.AppendEvent(e),
	))
}

const projectCols = `id, organization_id, key, name, description, created_at, updated_at, version`

// ProjectByKey returns the project with key.
func (s *Store) ProjectByKey(ctx context.Context, key string) (tenancy.Project, error) {
	p, err := scanProject(s.db.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE key = ?`, key))
	return p, mapReadErr("project "+key, err)
}

// ProjectByID returns the project with id.
func (s *Store) ProjectByID(ctx context.Context, id string) (tenancy.Project, error) {
	p, err := scanProject(s.db.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
	return p, mapReadErr("project "+id, err)
}

// ListProjects returns an organization's projects ordered by key.
func (s *Store) ListProjects(ctx context.Context, organizationID string) ([]tenancy.Project, error) {
	rows, err := s.db.Query(ctx, `SELECT `+projectCols+` FROM projects WHERE organization_id = ? ORDER BY key`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var out []tenancy.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanOrganization(r scanner) (tenancy.Organization, error) {
	var c tenancy.Organization
	var created, updated string
	if err := r.Scan(&c.ID, &c.Key, &c.Name, &c.FeaturePolicy, &created, &updated, &c.Version); err != nil {
		return tenancy.Organization{}, err
	}
	var err error
	if c.CreatedAt, err = parseTime(created); err != nil {
		return tenancy.Organization{}, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return tenancy.Organization{}, err
	}
	return c, nil
}

func scanProject(r scanner) (tenancy.Project, error) {
	var p tenancy.Project
	var created, updated string
	if err := r.Scan(&p.ID, &p.OrganizationID, &p.Key, &p.Name, &p.Description, &created, &updated, &p.Version); err != nil {
		return tenancy.Project{}, err
	}
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return tenancy.Project{}, err
	}
	if p.UpdatedAt, err = parseTime(updated); err != nil {
		return tenancy.Project{}, err
	}
	return p, nil
}

// policyOrDefault stores organizations without a feature policy as direct.
func policyOrDefault(p string) string {
	if p == "" {
		return "direct"
	}
	return p
}
