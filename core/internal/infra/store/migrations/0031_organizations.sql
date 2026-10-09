-- Customers are organizations (ADR-0027): tables, columns, indexes and
-- stored scopes, roles and events are renamed. Columns are renamed in
-- place (references, indexes and triggers follow); the tables whose CHECK
-- constraints name the scope kinds are rebuilt.
ALTER TABLE customers RENAME TO organizations;

ALTER TABLE projects RENAME COLUMN customer_id TO organization_id;
DROP INDEX projects_customer;
CREATE INDEX projects_organization ON projects (organization_id, key);

ALTER TABLE events RENAME COLUMN customer_id TO organization_id;
DROP INDEX events_customer;
CREATE INDEX events_organization ON events (organization_id, seq);
UPDATE events SET entity_type = 'organization' WHERE entity_type = 'customer';
UPDATE events SET type = 'organization.' || substr(type, length('customer.') + 1) WHERE type LIKE 'customer.%';

ALTER TABLE credentials RENAME COLUMN customer_id TO organization_id;

ALTER TABLE usage_records RENAME COLUMN customer_id TO organization_id;
DROP INDEX usage_customer;
CREATE INDEX usage_organization ON usage_records (organization_id, occurred_at);

ALTER TABLE search_docs RENAME COLUMN customer_key TO organization_key;

UPDATE budgets SET scope = 'organization:' || substr(scope, length('customer:') + 1) WHERE scope LIKE 'customer:%';

CREATE TABLE role_bindings_new (
    id                TEXT PRIMARY KEY,
    claim             TEXT NOT NULL,
    value             TEXT NOT NULL,
    role              TEXT NOT NULL,
    scope_kind        TEXT NOT NULL CHECK (scope_kind IN ('platform', 'organization', 'project')),
    organization_key  TEXT NOT NULL DEFAULT '',
    project_key       TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    UNIQUE (claim, value, role, scope_kind, organization_key, project_key)
);
INSERT INTO role_bindings_new
SELECT id, claim, value,
       CASE role WHEN 'customer-admin' THEN 'organization-admin' ELSE role END,
       CASE scope_kind WHEN 'customer' THEN 'organization' ELSE scope_kind END,
       customer_key, project_key, created_at
FROM role_bindings;
DROP TABLE role_bindings;
ALTER TABLE role_bindings_new RENAME TO role_bindings;

-- skill_versions references skills: set it aside while skills is rebuilt.
CREATE TEMP TABLE skill_versions_saved AS SELECT * FROM skill_versions;
DROP TABLE skill_versions;

CREATE TABLE skills_new (
    id                TEXT PRIMARY KEY,
    scope_kind        TEXT NOT NULL CHECK (scope_kind IN ('platform', 'organization', 'project')),
    organization_key  TEXT NOT NULL DEFAULT '',
    project_key       TEXT NOT NULL DEFAULT '',
    organization_id   TEXT NOT NULL DEFAULT '',
    name              TEXT NOT NULL,
    description       TEXT NOT NULL,
    body              TEXT NOT NULL,
    files             TEXT NOT NULL DEFAULT '{}',
    latest_version    INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    version           INTEGER NOT NULL,
    UNIQUE (scope_kind, organization_key, project_key, name)
);
INSERT INTO skills_new
SELECT id, CASE scope_kind WHEN 'customer' THEN 'organization' ELSE scope_kind END,
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

UPDATE search_docs SET scope = 'organization:' || substr(scope, length('customer:') + 1) WHERE scope LIKE 'customer:%';
