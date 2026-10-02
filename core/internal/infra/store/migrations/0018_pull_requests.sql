-- Forge of a project's repository, and tickets' pull requests (ADR-0007).
ALTER TABLE project_execution ADD COLUMN forge TEXT NOT NULL DEFAULT '';
ALTER TABLE project_execution ADD COLUMN forge_api_url TEXT NOT NULL DEFAULT '';
ALTER TABLE project_execution ADD COLUMN link_template TEXT NOT NULL DEFAULT '';

CREATE TABLE pull_requests (
    ticket_id   TEXT PRIMARY KEY REFERENCES items (id),
    project_id  TEXT NOT NULL REFERENCES projects (id),
    forge       TEXT NOT NULL,
    number      INTEGER NOT NULL,
    url         TEXT NOT NULL,
    title       TEXT NOT NULL,
    head        TEXT NOT NULL,
    base        TEXT NOT NULL,
    head_sha    TEXT NOT NULL,
    state       TEXT NOT NULL,
    draft       INTEGER NOT NULL,
    mergeable   INTEGER,
    checks      TEXT NOT NULL,
    review      TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX pull_requests_state ON pull_requests (state);
