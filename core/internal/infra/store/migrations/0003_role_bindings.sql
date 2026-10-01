CREATE TABLE role_bindings (
    id            TEXT PRIMARY KEY,
    claim         TEXT NOT NULL,
    value         TEXT NOT NULL,
    role          TEXT NOT NULL,
    scope_kind    TEXT NOT NULL CHECK (scope_kind IN ('organization', 'customer', 'project')),
    customer_key  TEXT NOT NULL DEFAULT '',
    project_key   TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    UNIQUE (claim, value, role, scope_kind, customer_key, project_key)
);
