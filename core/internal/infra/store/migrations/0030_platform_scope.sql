-- The installation-wide scope is the platform, no longer the organization
-- (ADR-0027): stored scopes, roles and pauses are rewritten, and the
-- tables whose CHECK constraints name the scope are rebuilt.
CREATE TABLE role_bindings_new (
    id            TEXT PRIMARY KEY,
    claim         TEXT NOT NULL,
    value         TEXT NOT NULL,
    role          TEXT NOT NULL,
    scope_kind    TEXT NOT NULL CHECK (scope_kind IN ('platform', 'customer', 'project')),
    customer_key  TEXT NOT NULL DEFAULT '',
    project_key   TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    UNIQUE (claim, value, role, scope_kind, customer_key, project_key)
);
INSERT INTO role_bindings_new
SELECT id, claim, value,
       CASE role WHEN 'org-admin' THEN 'platform-admin' ELSE role END,
       CASE scope_kind WHEN 'organization' THEN 'platform' ELSE scope_kind END,
       customer_key, project_key, created_at
FROM role_bindings;
DROP TABLE role_bindings;
ALTER TABLE role_bindings_new RENAME TO role_bindings;

-- skill_versions references skills: set it aside while skills is rebuilt.
CREATE TEMP TABLE skill_versions_saved AS SELECT * FROM skill_versions;
DROP TABLE skill_versions;

CREATE TABLE skills_new (
    id              TEXT PRIMARY KEY,
    scope_kind      TEXT NOT NULL CHECK (scope_kind IN ('platform', 'customer', 'project')),
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
INSERT INTO skills_new
SELECT id, CASE scope_kind WHEN 'organization' THEN 'platform' ELSE scope_kind END,
       customer_key, project_key, customer_id, name, description, body, files,
       latest_version, created_at, updated_at, version
FROM skills;
DROP TABLE skills;
ALTER TABLE skills_new RENAME TO skills;

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
INSERT INTO skill_versions SELECT * FROM skill_versions_saved;
DROP TABLE skill_versions_saved;

UPDATE search_docs SET scope = 'platform' WHERE scope = 'organization';
UPDATE pauses SET scope = 'platform' WHERE scope = 'org';
UPDATE events SET entity_type = 'platform', entity_id = 'platform'
WHERE entity_type = 'organization';
