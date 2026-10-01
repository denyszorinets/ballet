-- Agent skills (ADR-0010): the editable draft lives in skills; published
-- versions in skill_versions are immutable.
CREATE TABLE skills (
    id              TEXT PRIMARY KEY,
    scope_kind      TEXT NOT NULL CHECK (scope_kind IN ('organization', 'customer', 'project')),
    customer_key    TEXT NOT NULL DEFAULT '',
    project_key     TEXT NOT NULL DEFAULT '',
    customer_id     TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL,
    description     TEXT NOT NULL,
    body            TEXT NOT NULL,
    files           TEXT NOT NULL DEFAULT '{}',
    latest_version  INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    version         INTEGER NOT NULL,
    UNIQUE (scope_kind, customer_key, project_key, name)
);

CREATE TABLE skill_versions (
    skill_id      TEXT NOT NULL REFERENCES skills (id),
    number        INTEGER NOT NULL,
    description   TEXT NOT NULL,
    body          TEXT NOT NULL,
    files         TEXT NOT NULL,
    published_by  TEXT NOT NULL,
    published_at  TEXT NOT NULL,
    PRIMARY KEY (skill_id, number)
);
