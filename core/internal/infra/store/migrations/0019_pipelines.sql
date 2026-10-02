-- Pipeline definitions per project (ADR-0017): immutable versions per name
-- ("default" or a ticket type). The definition is JSON (domain/pipeline).
CREATE TABLE pipelines (
    project_id  TEXT NOT NULL REFERENCES projects (id),
    name        TEXT NOT NULL,
    version     INTEGER NOT NULL,
    definition  TEXT NOT NULL,
    created_by  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, name, version)
);
