-- A ticket's way through its pipeline (ADR-0014, ADR-0016). The pipeline
-- definition is snapshotted: the ticket runs on the version it started with.
CREATE TABLE flows (
    ticket_id         TEXT PRIMARY KEY REFERENCES items (id),
    project_id        TEXT NOT NULL REFERENCES projects (id),
    pipeline          TEXT NOT NULL,
    pipeline_version  INTEGER NOT NULL,
    definition        TEXT NOT NULL,
    stage             TEXT NOT NULL,
    iteration         INTEGER NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('running', 'waiting', 'done', 'failed', 'stopped')),
    waiting           TEXT NOT NULL DEFAULT '',
    run_id            TEXT NOT NULL DEFAULT '',
    outcome           TEXT NOT NULL DEFAULT '',
    report            TEXT NOT NULL DEFAULT '',
    started_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    version           INTEGER NOT NULL
);

CREATE INDEX flows_status ON flows (status);
