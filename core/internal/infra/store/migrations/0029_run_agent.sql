-- Runs are executed by agents (ADR-0025): the column names the agent.
ALTER TABLE runs RENAME COLUMN runner TO agent;
