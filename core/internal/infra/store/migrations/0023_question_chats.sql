-- A planner session can be a question's sub-chat (ADR-0015): scoped to the
-- question's ticket, with the question's context in its instructions.
ALTER TABLE planner_sessions ADD COLUMN question_id TEXT NOT NULL DEFAULT '';
ALTER TABLE planner_sessions ADD COLUMN context TEXT NOT NULL DEFAULT '';
CREATE INDEX planner_sessions_question ON planner_sessions (question_id) WHERE question_id <> '';
