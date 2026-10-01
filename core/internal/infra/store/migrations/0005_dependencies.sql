-- graph_version is bumped by every dependency change so that concurrent
-- additions cannot jointly create a cycle (optimistic concurrency).
ALTER TABLE projects ADD COLUMN graph_version INTEGER NOT NULL DEFAULT 0;

CREATE TABLE dependencies (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects (id),
    from_id     TEXT NOT NULL REFERENCES items (id),
    to_id       TEXT NOT NULL REFERENCES items (id),
    type        TEXT NOT NULL CHECK (type IN ('blocks', 'relates')),
    created_at  TEXT NOT NULL,
    UNIQUE (from_id, to_id, type)
);

CREATE INDEX dependencies_project ON dependencies (project_id);
CREATE INDEX dependencies_to ON dependencies (to_id, type);
CREATE INDEX dependencies_from ON dependencies (from_id, type);
