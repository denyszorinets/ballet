-- Durable jobs (ADR-0016): inserted in the batch of the transition that
-- causes them, claimed by the orchestrator with optimistic updates.
CREATE TABLE jobs (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL,
    dedupe_key    TEXT NOT NULL DEFAULT '',
    payload       TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('pending', 'running', 'done', 'dead')),
    run_at        TEXT NOT NULL,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL,
    lease_until   TEXT,
    last_error    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    version       INTEGER NOT NULL
);

CREATE INDEX jobs_due ON jobs (status, run_at);
-- At most one live job per dedupe key (timers, "check X" jobs).
CREATE UNIQUE INDEX jobs_dedupe ON jobs (dedupe_key) WHERE dedupe_key <> '' AND status IN ('pending', 'running');
