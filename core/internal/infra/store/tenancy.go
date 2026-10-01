package store

import (
	"context"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// CreateCustomer inserts c and records e.
func (s *Store) CreateCustomer(ctx context.Context, c tenancy.Customer, e event.Event) error {
	return mapWriteErr("create customer", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO customers (id, key, name, created_at, updated_at, version)
			VALUES (?, ?, ?, ?, ?, ?)`, c.ID, c.Key, c.Name, formatTime(c.CreatedAt), formatTime(c.UpdatedAt), c.Version),
		s.AppendEvent(e),
	))
}

// UpdateCustomer stores c if the stored version equals expectedVersion.
func (s *Store) UpdateCustomer(ctx context.Context, c tenancy.Customer, expectedVersion int64, e event.Event) error {
	return mapWriteErr("update customer", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE customers SET name = ?, updated_at = ?, version = ?
			WHERE id = ? AND version = ?`, c.Name, formatTime(c.UpdatedAt), c.Version, c.ID, expectedVersion),
		s.AppendEvent(e),
	))
}

const customerCols = `id, key, name, created_at, updated_at, version`

// CustomerByKey returns the customer with key.
func (s *Store) CustomerByKey(ctx context.Context, key string) (tenancy.Customer, error) {
	c, err := scanCustomer(s.db.QueryRow(ctx, `SELECT `+customerCols+` FROM customers WHERE key = ?`, key))
	return c, mapReadErr("customer "+key, err)
}

// CustomerByID returns the customer with id.
func (s *Store) CustomerByID(ctx context.Context, id string) (tenancy.Customer, error) {
	c, err := scanCustomer(s.db.QueryRow(ctx, `SELECT `+customerCols+` FROM customers WHERE id = ?`, id))
	return c, mapReadErr("customer "+id, err)
}

// ListCustomers returns all customers ordered by key.
func (s *Store) ListCustomers(ctx context.Context) ([]tenancy.Customer, error) {
	rows, err := s.db.Query(ctx, `SELECT `+customerCols+` FROM customers ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}
	defer rows.Close()
	var out []tenancy.Customer
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, fmt.Errorf("list customers: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateProject inserts p and records e.
func (s *Store) CreateProject(ctx context.Context, p tenancy.Project, e event.Event) error {
	return mapWriteErr("create project", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO projects (id, customer_id, key, name, description, created_at, updated_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, p.ID, p.CustomerID, p.Key, p.Name, p.Description,
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

const projectCols = `id, customer_id, key, name, description, created_at, updated_at, version`

// ProjectByKey returns the project with key.
func (s *Store) ProjectByKey(ctx context.Context, key string) (tenancy.Project, error) {
	p, err := scanProject(s.db.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE key = ?`, key))
	return p, mapReadErr("project "+key, err)
}

// ListProjects returns a customer's projects ordered by key.
func (s *Store) ListProjects(ctx context.Context, customerID string) ([]tenancy.Project, error) {
	rows, err := s.db.Query(ctx, `SELECT `+projectCols+` FROM projects WHERE customer_id = ? ORDER BY key`, customerID)
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

func scanCustomer(r scanner) (tenancy.Customer, error) {
	var c tenancy.Customer
	var created, updated string
	if err := r.Scan(&c.ID, &c.Key, &c.Name, &created, &updated, &c.Version); err != nil {
		return tenancy.Customer{}, err
	}
	var err error
	if c.CreatedAt, err = parseTime(created); err != nil {
		return tenancy.Customer{}, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return tenancy.Customer{}, err
	}
	return c, nil
}

func scanProject(r scanner) (tenancy.Project, error) {
	var p tenancy.Project
	var created, updated string
	if err := r.Scan(&p.ID, &p.CustomerID, &p.Key, &p.Name, &p.Description, &created, &updated, &p.Version); err != nil {
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
