-- LLM provider credentials; project_id '' is the customer default.
-- The API key is stored only as AES-GCM ciphertext.
CREATE TABLE credentials (
    id           TEXT PRIMARY KEY,
    customer_id  TEXT NOT NULL REFERENCES customers (id),
    project_id   TEXT NOT NULL DEFAULT '',
    provider     TEXT NOT NULL,
    ciphertext   TEXT NOT NULL,
    base_url     TEXT NOT NULL DEFAULT '',
    fingerprint  TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (customer_id, project_id, provider)
);
