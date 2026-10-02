-- Where an open question is being answered (ADR-0015): '' not routed yet,
-- 'planner' while the planner tries, 'human' in the humans' inbox.
ALTER TABLE questions ADD COLUMN route TEXT NOT NULL DEFAULT '';
