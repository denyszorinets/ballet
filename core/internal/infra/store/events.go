package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

// AppendEvent returns the statement recording e. Include it in the same
// batch as the change it describes. ID and OccurredAt are set if empty.
func (s *Store) AppendEvent(e event.Event) sqlstore.Stmt {
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = s.now()
	}
	var payload any
	if len(e.Payload) > 0 {
		payload = string(e.Payload)
	}
	return sqlstore.Exec(`INSERT INTO events
		(id, occurred_at, customer_id, project_id, entity_type, entity_id, type, actor_kind, actor_sub, acting_for, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, formatTime(e.OccurredAt), nullable(e.Customer), nullable(e.Project),
		e.EntityType, e.EntityID, e.Type, string(e.Actor.Kind), e.Actor.Subject, nullable(e.Actor.ActingFor), payload)
}

// EventFilter selects events. Zero fields do not filter.
type EventFilter struct {
	Customer   string
	Project    string
	EntityType string
	EntityID   string
	AfterSeq   int64 // only events with a greater seq
	Limit      int   // default and maximum 1000
}

const maxEvents = 1000

// ListEvents returns matching events in seq order.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]event.Event, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		where = append(where, cond)
		args = append(args, v)
	}
	if f.Customer != "" {
		add("customer_id = ?", f.Customer)
	}
	if f.Project != "" {
		add("project_id = ?", f.Project)
	}
	if f.EntityType != "" {
		add("entity_type = ?", f.EntityType)
	}
	if f.EntityID != "" {
		add("entity_id = ?", f.EntityID)
	}
	if f.AfterSeq > 0 {
		add("seq > ?", f.AfterSeq)
	}
	limit := f.Limit
	if limit <= 0 || limit > maxEvents {
		limit = maxEvents
	}
	q := `SELECT seq, id, occurred_at, customer_id, project_id, entity_type, entity_id, type,
		actor_kind, actor_sub, acting_for, payload FROM events`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY seq LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var out []event.Event
	for rows.Next() {
		var e event.Event
		var occurred, actorKind string
		var customer, project, actingFor, payload sql.NullString
		if err := rows.Scan(&e.Seq, &e.ID, &occurred, &customer, &project, &e.EntityType, &e.EntityID,
			&e.Type, &actorKind, &e.Actor.Subject, &actingFor, &payload); err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
		if e.OccurredAt, err = parseTime(occurred); err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
		e.Customer, e.Project = customer.String, project.String
		e.Actor.Kind, e.Actor.ActingFor = event.ActorKind(actorKind), actingFor.String
		if payload.Valid {
			e.Payload = []byte(payload.String)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return out, nil
}

// LastEventSeq returns the highest event seq, or 0 when there are none.
func (s *Store) LastEventSeq(ctx context.Context) (int64, error) {
	var seq sql.NullInt64
	if err := s.db.QueryRow(ctx, "SELECT max(seq) FROM events").Scan(&seq); err != nil {
		return 0, fmt.Errorf("last event seq: %w", err)
	}
	return seq.Int64, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// EntityHistory returns all events of one entity in seq order.
func (s *Store) EntityHistory(ctx context.Context, entityType, entityID string) ([]event.Event, error) {
	return s.ListEvents(ctx, EventFilter{EntityType: entityType, EntityID: entityID})
}
