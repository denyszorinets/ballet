-- Runs: agent sessions executed by Runners (ADR-0009), and their output.
CREATE TABLE runs (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects (id),
    ticket_id    TEXT NOT NULL REFERENCES items (id),
    stage        TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('queued', 'starting', 'running', 'succeeded', 'failed', 'cancelled')),
    spec         TEXT NOT NULL,
    runner       TEXT NOT NULL DEFAULT '',
    exit_code    INTEGER,
    error        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    started_at   TEXT,
    finished_at  TEXT,
    version      INTEGER NOT NULL,
    log_seq      INTEGER NOT NULL DEFAULT 1,
    log_bytes    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX runs_ticket ON runs (ticket_id, created_at);
CREATE INDEX runs_status ON runs (status, created_at);

CREATE TABLE run_logs (
    run_id  TEXT NOT NULL REFERENCES runs (id),
    seq     INTEGER NOT NULL,
    stream  TEXT NOT NULL,
    text    TEXT NOT NULL,
    at      TEXT NOT NULL,
    PRIMARY KEY (run_id, seq)
);
