-- Human review of assumptions agents recorded (assumption register):
-- '' not reviewed, 'confirmed' or 'rejected'; follow_up is the question or
-- changeset a rejection created.
ALTER TABLE agent_reports ADD COLUMN review TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_reports ADD COLUMN review_comment TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_reports ADD COLUMN reviewed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_reports ADD COLUMN reviewed_at TEXT;
ALTER TABLE agent_reports ADD COLUMN follow_up TEXT NOT NULL DEFAULT '';
CREATE INDEX agent_reports_assumptions ON agent_reports (project_id, kind, review);
