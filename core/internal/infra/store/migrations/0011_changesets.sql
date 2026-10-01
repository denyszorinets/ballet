-- Plan changesets: proposed planning changes approved by a human. Operations,
-- approved indices and per-operation results are JSON (domain/changeset).
CREATE TABLE changesets (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL REFERENCES projects (id),
    title         TEXT NOT NULL,
    summary       TEXT NOT NULL,
    ops           TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('proposed', 'applied', 'rejected')),
    proposed_by   TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    decided_by    TEXT,
    decided_at    TEXT,
    approved      TEXT NOT NULL DEFAULT '[]',
    results       TEXT NOT NULL DEFAULT '[]',
    version       INTEGER NOT NULL
);

CREATE INDEX changesets_project ON changesets (project_id, created_at);
