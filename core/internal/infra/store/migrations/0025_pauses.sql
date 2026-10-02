-- Humans pausing autonomous work: scope 'org' (everything) or a project ID.
CREATE TABLE pauses (
    scope      TEXT PRIMARY KEY,
    reason     TEXT NOT NULL DEFAULT '',
    paused_by  TEXT NOT NULL,
    paused_at  TEXT NOT NULL
);
