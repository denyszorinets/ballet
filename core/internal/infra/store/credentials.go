package store

import (
	"context"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/credential"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// SetCredential inserts or replaces the credential of its scope and records e.
func (s *Store) SetCredential(ctx context.Context, c credential.Credential, ciphertext string, e event.Event) error {
	return mapWriteErr("set credential", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO credentials (id, customer_id, project_id, provider, ciphertext, base_url, fingerprint, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (customer_id, project_id, provider) DO UPDATE SET
				ciphertext = excluded.ciphertext, base_url = excluded.base_url,
				fingerprint = excluded.fingerprint, updated_at = excluded.updated_at`,
			c.ID, c.CustomerID, c.ProjectID, string(c.Provider), ciphertext, c.BaseURL, c.Fingerprint,
			formatTime(c.CreatedAt), formatTime(c.UpdatedAt)),
		s.AppendEvent(e),
	))
}

// DeleteCredential deletes the credential of a scope and records e.
func (s *Store) DeleteCredential(ctx context.Context, customerID, projectID string, p credential.Provider, e event.Event) error {
	return mapWriteErr("delete credential", s.db.Batch(ctx,
		sqlstore.ExecOne(`DELETE FROM credentials WHERE customer_id = ? AND project_id = ? AND provider = ?`,
			customerID, projectID, string(p)),
		s.AppendEvent(e),
	))
}

const credentialCols = `id, customer_id, project_id, provider, base_url, fingerprint, created_at, updated_at`

// ListCredentials returns a customer's credentials without secrets.
func (s *Store) ListCredentials(ctx context.Context, customerID string) ([]credential.Credential, error) {
	rows, err := s.db.Query(ctx, `SELECT `+credentialCols+` FROM credentials WHERE customer_id = ?
		ORDER BY project_id, provider`, customerID)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()
	var out []credential.Credential
	for rows.Next() {
		var c credential.Credential
		var provider, created, updated string
		if err := rows.Scan(&c.ID, &c.CustomerID, &c.ProjectID, &provider, &c.BaseURL, &c.Fingerprint, &created, &updated); err != nil {
			return nil, fmt.Errorf("list credentials: %w", err)
		}
		c.Provider = credential.Provider(provider)
		if c.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if c.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CredentialCiphertext returns the credential of an exact scope with its
// ciphertext.
func (s *Store) CredentialCiphertext(ctx context.Context, customerID, projectID string, p credential.Provider) (credential.Credential, string, error) {
	var c credential.Credential
	var provider, ciphertext string
	err := s.db.QueryRow(ctx, `SELECT id, customer_id, project_id, provider, base_url, fingerprint, ciphertext
		FROM credentials WHERE customer_id = ? AND project_id = ? AND provider = ?`, customerID, projectID, string(p)).
		Scan(&c.ID, &c.CustomerID, &c.ProjectID, &provider, &c.BaseURL, &c.Fingerprint, &ciphertext)
	c.Provider = credential.Provider(provider)
	return c, ciphertext, mapReadErr("credential", err)
}
