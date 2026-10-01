-- Milestones, epics and tickets share one table and one per-project key
-- sequence (projects.next_item_number).
ALTER TABLE projects ADD COLUMN next_item_number INTEGER NOT NULL DEFAULT 1;

CREATE TABLE items (
    id                   TEXT PRIMARY KEY,
    project_id           TEXT NOT NULL REFERENCES projects (id),
    number               INTEGER NOT NULL,
    key                  TEXT NOT NULL UNIQUE,
    kind                 TEXT NOT NULL CHECK (kind IN ('milestone', 'epic', 'ticket')),
    title                TEXT NOT NULL,
    description          TEXT NOT NULL DEFAULT '',
    state                TEXT NOT NULL,
    stage                TEXT NOT NULL DEFAULT '',
    type                 TEXT NOT NULL DEFAULT '',
    acceptance_criteria  TEXT NOT NULL DEFAULT '[]',
    review_mode          TEXT NOT NULL DEFAULT '',
    merge_mode           TEXT NOT NULL DEFAULT '',
    epic_id              TEXT REFERENCES items (id),
    milestone_id         TEXT REFERENCES items (id),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    version              INTEGER NOT NULL,
    UNIQUE (project_id, number)
);

CREATE INDEX items_project ON items (project_id, kind, number);
CREATE INDEX items_epic ON items (epic_id);
CREATE INDEX items_milestone ON items (milestone_id);
