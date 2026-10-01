-- Search documents derived from Core entities (items, skills), with a
-- full-text index kept in sync by triggers and embeddings for semantic
-- search (ADR-0021). doc id: "<kind>:<entity id>".
CREATE TABLE search_docs (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL,
    entity_id     TEXT NOT NULL,
    ref           TEXT NOT NULL,  -- item key or skill name
    customer_key  TEXT NOT NULL DEFAULT '',
    project_key   TEXT NOT NULL DEFAULT '',
    scope         TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL,
    body          TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE VIRTUAL TABLE search_fts USING fts5(title, body, content='search_docs', content_rowid='rowid');

CREATE TRIGGER search_docs_ai AFTER INSERT ON search_docs BEGIN
    INSERT INTO search_fts (rowid, title, body) VALUES (new.rowid, new.title, new.body);
END;
CREATE TRIGGER search_docs_ad AFTER DELETE ON search_docs BEGIN
    INSERT INTO search_fts (search_fts, rowid, title, body) VALUES ('delete', old.rowid, old.title, old.body);
END;
CREATE TRIGGER search_docs_au AFTER UPDATE OF title, body ON search_docs BEGIN
    INSERT INTO search_fts (search_fts, rowid, title, body) VALUES ('delete', old.rowid, old.title, old.body);
    INSERT INTO search_fts (rowid, title, body) VALUES (new.rowid, new.title, new.body);
END;

CREATE TABLE search_embeddings (
    doc_id        TEXT PRIMARY KEY REFERENCES search_docs (id),
    model         TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    vector        BLOB NOT NULL
);

-- Indexer cursor over the event log.
CREATE TABLE search_state (
    name   TEXT PRIMARY KEY,
    value  INTEGER NOT NULL
);
