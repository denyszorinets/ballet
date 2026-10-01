package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

const sessionCols = `id, project_id, title, created_by, created_at, updated_at, summary, summary_upto`

// CreatePlannerSession inserts a session and records e.
func (s *Store) CreatePlannerSession(ctx context.Context, ps planner.Session, e event.Event) error {
	return mapWriteErr("create planner session", s.db.Batch(ctx,
		sqlstore.Exec(`INSERT INTO planner_sessions (`+sessionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ps.ID, ps.ProjectID, ps.Title, ps.CreatedBy, formatTime(ps.CreatedAt), formatTime(ps.UpdatedAt),
			ps.Summary, ps.SummaryUpTo),
		s.AppendEvent(e),
	))
}

// PlannerSession returns the session with id.
func (s *Store) PlannerSession(ctx context.Context, id string) (planner.Session, error) {
	ps, err := scanSession(s.db.QueryRow(ctx, `SELECT `+sessionCols+` FROM planner_sessions WHERE id = ?`, id))
	return ps, mapReadErr("planner session "+id, err)
}

// ListPlannerSessions returns a project's sessions, most recently active first.
func (s *Store) ListPlannerSessions(ctx context.Context, projectID string) ([]planner.Session, error) {
	rows, err := s.db.Query(ctx, `SELECT `+sessionCols+` FROM planner_sessions WHERE project_id = ?
		ORDER BY updated_at DESC, id DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list planner sessions: %w", err)
	}
	defer rows.Close()
	var out []planner.Session
	for rows.Next() {
		ps, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("list planner sessions: %w", err)
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// AppendPlannerMessage stores m as the session's next message (its Seq is
// assigned in the same batch) and records e. Returns the stored message.
func (s *Store) AppendPlannerMessage(ctx context.Context, m planner.Message, e event.Event) (planner.Message, error) {
	content, err := json.Marshal(m.Content)
	if err != nil {
		return planner.Message{}, fmt.Errorf("append planner message: %w", err)
	}
	usage, err := json.Marshal(m.Usage)
	if err != nil {
		return planner.Message{}, fmt.Errorf("append planner message: %w", err)
	}
	var seq int64
	if err := s.db.QueryRow(ctx, `SELECT next_seq FROM planner_sessions WHERE id = ?`, m.SessionID).Scan(&seq); err != nil {
		return planner.Message{}, mapReadErr("planner session "+m.SessionID, err)
	}
	// The guard on next_seq serialises concurrent appends: the loser gets
	// ErrConflict. The planner runs one turn per session at a time.
	err = s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE planner_sessions SET next_seq = next_seq + 1, updated_at = ? WHERE id = ? AND next_seq = ?`,
			formatTime(m.CreatedAt), m.SessionID, seq),
		sqlstore.Exec(`INSERT INTO planner_messages (session_id, seq, role, content, author, stop_reason, usage, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.SessionID, seq, string(m.Role), string(content), m.Author, m.StopReason, string(usage), formatTime(m.CreatedAt)),
		s.AppendEvent(e),
	)
	if err != nil {
		return planner.Message{}, mapWriteErr("append planner message", err)
	}
	m.Seq = seq
	return m, nil
}

// SetPlannerSummary stores a session's compaction summary covering the
// messages up to seq upTo, and records e.
func (s *Store) SetPlannerSummary(ctx context.Context, sessionID, summary string, upTo int64, e event.Event) error {
	return mapWriteErr("set planner summary", s.db.Batch(ctx,
		sqlstore.ExecOne(`UPDATE planner_sessions SET summary = ?, summary_upto = ? WHERE id = ?`, summary, upTo, sessionID),
		s.AppendEvent(e),
	))
}

// PlannerMessages returns a session's transcript in order.
func (s *Store) PlannerMessages(ctx context.Context, sessionID string) ([]planner.Message, error) {
	rows, err := s.db.Query(ctx, `SELECT seq, role, content, author, stop_reason, usage, created_at
		FROM planner_messages WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("planner messages: %w", err)
	}
	defer rows.Close()
	var out []planner.Message
	for rows.Next() {
		m := planner.Message{SessionID: sessionID}
		var role, content, usage, created string
		if err := rows.Scan(&m.Seq, &role, &content, &m.Author, &m.StopReason, &usage, &created); err != nil {
			return nil, fmt.Errorf("planner messages: %w", err)
		}
		m.Role = planner.Role(role)
		if err := json.Unmarshal([]byte(content), &m.Content); err != nil {
			return nil, fmt.Errorf("planner messages: %w", err)
		}
		if err := json.Unmarshal([]byte(usage), &m.Usage); err != nil {
			return nil, fmt.Errorf("planner messages: %w", err)
		}
		if m.CreatedAt, err = parseTime(created); err != nil {
			return nil, fmt.Errorf("planner messages: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanSession(r scanner) (planner.Session, error) {
	var ps planner.Session
	var created, updated string
	if err := r.Scan(&ps.ID, &ps.ProjectID, &ps.Title, &ps.CreatedBy, &created, &updated, &ps.Summary, &ps.SummaryUpTo); err != nil {
		return planner.Session{}, err
	}
	var err error
	if ps.CreatedAt, err = parseTime(created); err != nil {
		return planner.Session{}, err
	}
	ps.UpdatedAt, err = parseTime(updated)
	return ps, err
}
