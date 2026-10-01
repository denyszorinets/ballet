package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// CreateItem inserts it and records e. The item's number and key are taken
// from the project's sequence inside the same atomic batch, so concurrent
// creations never collide. Returns the stored item.
func (s *Store) CreateItem(ctx context.Context, it tracker.Item, e event.Event) (tracker.Item, error) {
	criteria, err := json.Marshal(nonNil(it.AcceptanceCriteria))
	if err != nil {
		return tracker.Item{}, fmt.Errorf("create item: %w", err)
	}
	err = s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE projects SET next_item_number = next_item_number + 1 WHERE id = ?`, it.ProjectID),
		sqlstore.Exec(`INSERT INTO items (id, project_id, number, key, kind, title, description, state, stage, type,
				acceptance_criteria, review_mode, merge_mode, epic_id, milestone_id, created_at, updated_at, version)
			SELECT ?, p.id, p.next_item_number - 1, p.key || '-' || (p.next_item_number - 1),
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
			FROM projects p WHERE p.id = ?`,
			it.ID, string(it.Kind), it.Title, it.Description, string(it.State), it.Stage, string(it.Type),
			string(criteria), string(it.Policy.ReviewMode), string(it.Policy.MergeMode),
			nullable(it.EpicID), nullable(it.MilestoneID), formatTime(it.CreatedAt), formatTime(it.UpdatedAt),
			it.Version, it.ProjectID),
		s.AppendEvent(e),
	)
	if err != nil {
		return tracker.Item{}, mapWriteErr("create item", err)
	}
	return s.ItemByID(ctx, it.ID)
}

// UpdateItem stores it if the stored version equals expectedVersion.
func (s *Store) UpdateItem(ctx context.Context, it tracker.Item, expectedVersion int64, e event.Event) error {
	criteria, err := json.Marshal(nonNil(it.AcceptanceCriteria))
	if err != nil {
		return fmt.Errorf("update item: %w", err)
	}
	return mapWriteErr("update item", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE items SET title = ?, description = ?, state = ?, stage = ?, type = ?,
				acceptance_criteria = ?, review_mode = ?, merge_mode = ?, epic_id = ?, milestone_id = ?,
				updated_at = ?, version = ?
			WHERE id = ? AND version = ?`,
			it.Title, it.Description, string(it.State), it.Stage, string(it.Type), string(criteria),
			string(it.Policy.ReviewMode), string(it.Policy.MergeMode), nullable(it.EpicID), nullable(it.MilestoneID),
			formatTime(it.UpdatedAt), it.Version, it.ID, expectedVersion),
		s.AppendEvent(e),
	))
}

const itemCols = `id, project_id, number, key, kind, title, description, state, stage, type, acceptance_criteria,
	review_mode, merge_mode, epic_id, milestone_id, created_at, updated_at, version`

// ItemByID returns the item with id.
func (s *Store) ItemByID(ctx context.Context, id string) (tracker.Item, error) {
	it, err := scanItem(s.db.QueryRow(ctx, `SELECT `+itemCols+` FROM items WHERE id = ?`, id))
	return it, mapReadErr("item "+id, err)
}

// ItemByKey returns the item with key (e.g. "ACME-42").
func (s *Store) ItemByKey(ctx context.Context, key string) (tracker.Item, error) {
	it, err := scanItem(s.db.QueryRow(ctx, `SELECT `+itemCols+` FROM items WHERE key = ?`, key))
	return it, mapReadErr("item "+key, err)
}

// ListItems returns a project's items matching f, ordered by number.
func (s *Store) ListItems(ctx context.Context, projectID string, f app.ItemFilter) ([]tracker.Item, error) {
	where := []string{"project_id = ?"}
	args := []any{projectID}
	if f.Kind != "" {
		where, args = append(where, "kind = ?"), append(args, string(f.Kind))
	}
	if f.State != "" {
		where, args = append(where, "state = ?"), append(args, string(f.State))
	}
	if f.EpicID != "" {
		where, args = append(where, "epic_id = ?"), append(args, f.EpicID)
	}
	if f.MilestoneID != "" {
		where, args = append(where, "milestone_id = ?"), append(args, f.MilestoneID)
	}
	rows, err := s.db.Query(ctx, `SELECT `+itemCols+` FROM items WHERE `+strings.Join(where, " AND ")+` ORDER BY number`, args...)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()
	var out []tracker.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("list items: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func scanItem(r scanner) (tracker.Item, error) {
	var it tracker.Item
	var kind, state, typ, criteria, review, merge, created, updated string
	var epic, milestone sql.NullString
	if err := r.Scan(&it.ID, &it.ProjectID, &it.Number, &it.Key, &kind, &it.Title, &it.Description, &state,
		&it.Stage, &typ, &criteria, &review, &merge, &epic, &milestone, &created, &updated, &it.Version); err != nil {
		return tracker.Item{}, err
	}
	it.Kind, it.State, it.Type = tracker.Kind(kind), tracker.State(state), tracker.TicketType(typ)
	it.Policy = tracker.Policy{ReviewMode: tracker.ReviewMode(review), MergeMode: tracker.MergeMode(merge)}
	it.EpicID, it.MilestoneID = epic.String, milestone.String
	if err := json.Unmarshal([]byte(criteria), &it.AcceptanceCriteria); err != nil {
		return tracker.Item{}, err
	}
	if len(it.AcceptanceCriteria) == 0 {
		it.AcceptanceCriteria = nil
	}
	var err error
	if it.CreatedAt, err = parseTime(created); err != nil {
		return tracker.Item{}, err
	}
	it.UpdatedAt, err = parseTime(updated)
	return it, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
