-- The feature map (ADR-0028): an organization's features, their immutable
-- revisions, the projects implementing them and the typed links between
-- them. A link is valid from created_at until removed_at.
ALTER TABLE organizations ADD COLUMN next_feature_number INTEGER NOT NULL DEFAULT 1;

CREATE TABLE features (
    id               TEXT PRIMARY KEY,
    organization_id  TEXT NOT NULL REFERENCES organizations (id),
    number           INTEGER NOT NULL,
    key              TEXT NOT NULL,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    version          INTEGER NOT NULL,
    UNIQUE (organization_id, number),
    UNIQUE (organization_id, key)
);

CREATE TABLE feature_projects (
    feature_id  TEXT NOT NULL REFERENCES features (id),
    project_id  TEXT NOT NULL REFERENCES projects (id),
    PRIMARY KEY (feature_id, project_id)
);
CREATE INDEX feature_projects_project ON feature_projects (project_id);

CREATE TABLE feature_revisions (
    feature_id   TEXT NOT NULL REFERENCES features (id),
    number       INTEGER NOT NULL,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL,
    status       TEXT NOT NULL,
    projects     TEXT NOT NULL DEFAULT '[]', -- project IDs
    author_kind  TEXT NOT NULL,
    author_sub   TEXT NOT NULL,
    acting_for   TEXT NOT NULL DEFAULT '',
    reason       TEXT NOT NULL DEFAULT '',
    cause_kind   TEXT NOT NULL DEFAULT '',
    cause_ref    TEXT NOT NULL DEFAULT '',
    review       TEXT NOT NULL DEFAULT '',
    reviewed_by  TEXT NOT NULL DEFAULT '',
    reviewed_at  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    PRIMARY KEY (feature_id, number)
);
CREATE INDEX feature_revisions_created ON feature_revisions (created_at);
CREATE INDEX feature_revisions_review ON feature_revisions (review);

CREATE TABLE feature_links (
    id               TEXT PRIMARY KEY,
    organization_id  TEXT NOT NULL REFERENCES organizations (id),
    from_id          TEXT NOT NULL REFERENCES features (id),
    to_id            TEXT NOT NULL REFERENCES features (id),
    type             TEXT NOT NULL,
    created_kind     TEXT NOT NULL,
    created_sub      TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    removed_kind     TEXT NOT NULL DEFAULT '',
    removed_sub      TEXT NOT NULL DEFAULT '',
    removed_at       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX feature_links_organization ON feature_links (organization_id, created_at);
CREATE INDEX feature_links_from ON feature_links (from_id);
CREATE INDEX feature_links_to ON feature_links (to_id);
