CREATE TABLE customers (
    id          TEXT PRIMARY KEY,
    key         TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    version     INTEGER NOT NULL
);

CREATE TABLE projects (
    id           TEXT PRIMARY KEY,
    customer_id  TEXT NOT NULL REFERENCES customers (id),
    key          TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    version      INTEGER NOT NULL
);

CREATE INDEX projects_customer ON projects (customer_id, key);
