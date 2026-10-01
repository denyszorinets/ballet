-- Planner chat sessions and their transcripts (ADR-0020). Message content
-- and usage are JSON (domain/planner).
CREATE TABLE planner_sessions (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects (id),
    title       TEXT NOT NULL,
    created_by  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    next_seq    INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX planner_sessions_project ON planner_sessions (project_id, updated_at);

CREATE TABLE planner_messages (
    session_id   TEXT NOT NULL REFERENCES planner_sessions (id),
    seq          INTEGER NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content      TEXT NOT NULL,
    author       TEXT NOT NULL DEFAULT '',
    stop_reason  TEXT NOT NULL DEFAULT '',
    usage        TEXT NOT NULL DEFAULT '{}',
    created_at   TEXT NOT NULL,
    PRIMARY KEY (session_id, seq)
);
