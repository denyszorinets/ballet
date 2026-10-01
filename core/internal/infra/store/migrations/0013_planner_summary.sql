-- Context compaction (planner): the model sees summary + the messages
-- after summary_upto; the transcript itself stays complete.
ALTER TABLE planner_sessions ADD COLUMN summary TEXT NOT NULL DEFAULT '';
ALTER TABLE planner_sessions ADD COLUMN summary_upto INTEGER NOT NULL DEFAULT 0;
