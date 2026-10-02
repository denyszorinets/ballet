-- Token budgets of unattended work: scope 'customer:<id>' or
-- 'project:<id>'; 0 means no limit.
CREATE TABLE budgets (
    scope          TEXT PRIMARY KEY,
    ticket_tokens  INTEGER NOT NULL DEFAULT 0,
    daily_tokens   INTEGER NOT NULL DEFAULT 0,
    updated_by     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    version        INTEGER NOT NULL
);

CREATE INDEX usage_customer ON usage_records (customer_id, occurred_at);
