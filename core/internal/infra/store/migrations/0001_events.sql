-- Append-only event log. seq is monotonic and never reused (AUTOINCREMENT),
-- so consumers can resume from the last seq they saw.
CREATE TABLE events (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    occurred_at  TEXT NOT NULL,
    customer_id  TEXT,
    project_id   TEXT,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    type         TEXT NOT NULL,
    actor_kind   TEXT NOT NULL,
    actor_sub    TEXT NOT NULL,
    acting_for   TEXT,
    payload      TEXT
);

CREATE INDEX events_entity ON events (entity_type, entity_id, seq);
CREATE INDEX events_project ON events (project_id, seq);
CREATE INDEX events_customer ON events (customer_id, seq);
