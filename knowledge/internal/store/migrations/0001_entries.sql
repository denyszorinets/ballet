-- Knowledge entries; customer is the knowledge space (customer key).
CREATE TABLE entries (
    id          TEXT PRIMARY KEY,
    customer    TEXT NOT NULL,
    kind        TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    projects    TEXT NOT NULL DEFAULT '[]',
    items       TEXT NOT NULL DEFAULT '[]',
    version     INTEGER NOT NULL,
    created_by  TEXT NOT NULL,
    updated_by  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX entries_customer ON entries (customer, kind, updated_at);

-- Every version of every entry, immutable.
CREATE TABLE entry_versions (
    entry_id    TEXT NOT NULL REFERENCES entries (id),
    version     INTEGER NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    author      TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (entry_id, version)
);
