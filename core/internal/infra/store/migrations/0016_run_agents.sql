-- Agent runs: the adapter that built the session and the result it
-- reported (JSON, empty until finished).
ALTER TABLE runs ADD COLUMN adapter TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN result TEXT NOT NULL DEFAULT '';
