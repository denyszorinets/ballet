package store

import (
	"context"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

const bindingCols = `id, claim, value, role, scope_kind, customer_key, project_key, created_at`

// ListRoleBindings returns all stored bindings in creation order.
func (s *Store) ListRoleBindings(ctx context.Context) ([]rbac.Binding, error) {
	rows, err := s.db.Query(ctx, `SELECT `+bindingCols+` FROM role_bindings ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list role bindings: %w", err)
	}
	defer rows.Close()
	var out []rbac.Binding
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, fmt.Errorf("list role bindings: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RoleBinding returns the binding with id.
func (s *Store) RoleBinding(ctx context.Context, id string) (rbac.Binding, error) {
	b, err := scanBinding(s.db.QueryRow(ctx, `SELECT `+bindingCols+` FROM role_bindings WHERE id = ?`, id))
	return b, mapReadErr("role binding "+id, err)
}

// CreateRoleBinding inserts b and records e.
func (s *Store) CreateRoleBinding(ctx context.Context, b rbac.Binding, e event.Event) error {
	return mapWriteErr("create role binding", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO role_bindings (`+bindingCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			b.ID, b.Claim, b.Value, string(b.Role), string(b.Scope.Kind), b.Scope.Customer, b.Scope.Project,
			formatTime(b.CreatedAt)),
		s.AppendEvent(e),
	))
}

// DeleteRoleBinding deletes the binding with id and records e.
func (s *Store) DeleteRoleBinding(ctx context.Context, id string, e event.Event) error {
	return mapWriteErr("delete role binding", s.db.Batch(ctx,
		sqlstore.ExecOne(`DELETE FROM role_bindings WHERE id = ?`, id),
		s.AppendEvent(e),
	))
}

func scanBinding(r scanner) (rbac.Binding, error) {
	var b rbac.Binding
	var role, kind, created string
	if err := r.Scan(&b.ID, &b.Claim, &b.Value, &role, &kind, &b.Scope.Customer, &b.Scope.Project, &created); err != nil {
		return rbac.Binding{}, err
	}
	b.Role, b.Scope.Kind = rbac.Role(role), rbac.ScopeKind(kind)
	var err error
	b.CreatedAt, err = parseTime(created)
	return b, err
}
