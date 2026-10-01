-- LLM usage per request, reported by the gateway. High volume: kept out
-- of the event log.
CREATE TABLE usage_records (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at         TEXT NOT NULL,
    customer_id         TEXT NOT NULL,
    project_id          TEXT NOT NULL,
    ticket_key          TEXT NOT NULL DEFAULT '',
    run                 TEXT NOT NULL DEFAULT '',
    model               TEXT NOT NULL DEFAULT '',
    status              INTEGER NOT NULL,
    input_tokens        INTEGER NOT NULL,
    output_tokens       INTEGER NOT NULL,
    cache_read_tokens   INTEGER NOT NULL,
    cache_write_tokens  INTEGER NOT NULL
);

CREATE INDEX usage_project ON usage_records (project_id, occurred_at);
CREATE INDEX usage_ticket ON usage_records (project_id, ticket_key);
