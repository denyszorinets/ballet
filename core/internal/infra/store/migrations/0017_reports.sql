-- What agent runs report about their ticket (domain/report).
CREATE TABLE agent_reports (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects (id),
    ticket_id   TEXT NOT NULL REFERENCES items (id),
    run_id      TEXT NOT NULL REFERENCES runs (id),
    kind        TEXT NOT NULL CHECK (kind IN ('progress', 'stage_report', 'assumption')),
    outcome     TEXT NOT NULL DEFAULT '',
    text        TEXT NOT NULL,
    detail      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);

CREATE INDEX agent_reports_ticket ON agent_reports (ticket_id, created_at);
CREATE INDEX agent_reports_run ON agent_reports (run_id, created_at);

CREATE TABLE questions (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects (id),
    ticket_id    TEXT NOT NULL REFERENCES items (id),
    run_id       TEXT NOT NULL DEFAULT '',
    text         TEXT NOT NULL,
    context      TEXT NOT NULL DEFAULT '',
    blocking     INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL CHECK (status IN ('open', 'answered')),
    answer       TEXT NOT NULL DEFAULT '',
    answered_by  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    answered_at  TEXT
);

CREATE INDEX questions_ticket ON questions (ticket_id, created_at);
CREATE INDEX questions_open ON questions (project_id, status);
