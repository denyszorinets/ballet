-- Full-text index over entries (external content, kept in sync by
-- triggers so every write path is covered).
CREATE VIRTUAL TABLE entries_fts USING fts5(title, body, content='entries', content_rowid='rowid');

INSERT INTO entries_fts (rowid, title, body) SELECT rowid, title, body FROM entries;

CREATE TRIGGER entries_ai AFTER INSERT ON entries BEGIN
    INSERT INTO entries_fts (rowid, title, body) VALUES (new.rowid, new.title, new.body);
END;
CREATE TRIGGER entries_ad AFTER DELETE ON entries BEGIN
    INSERT INTO entries_fts (entries_fts, rowid, title, body) VALUES ('delete', old.rowid, old.title, old.body);
END;
CREATE TRIGGER entries_au AFTER UPDATE OF title, body ON entries BEGIN
    INSERT INTO entries_fts (entries_fts, rowid, title, body) VALUES ('delete', old.rowid, old.title, old.body);
    INSERT INTO entries_fts (rowid, title, body) VALUES (new.rowid, new.title, new.body);
END;

-- One embedding per entry; content_hash detects stale vectors, model
-- detects a changed embedding model.
CREATE TABLE entry_embeddings (
    entry_id      TEXT PRIMARY KEY REFERENCES entries (id),
    customer      TEXT NOT NULL,
    model         TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    vector        BLOB NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE INDEX entry_embeddings_customer ON entry_embeddings (customer, model);
